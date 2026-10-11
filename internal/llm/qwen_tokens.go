package llm

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

// The installed GGUF must match this verified Qwen3 chat template. BPE counts
// content; the chat wrapper still has a separately labelled safety reserve.
const qwenTemplateHash = "ae370d884f108d16e7cc8fd5259ebc5773a0afa6e078b11f4ed7e39a27e0dfc4"
const qwenPattern = `(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\r\n\p{L}\p{N}]?\p{L}+|\p{N}| ?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+`

type ollamaShow struct {
	ModelInfo map[string]any `json:"model_info"`
	Template  string         `json:"template"`
}
type tokenPair struct{ left, right string }
type qwenTokenizer struct {
	ranks   map[tokenPair]int
	special []string
	pattern *regexp2.Regexp
}

func buildQwenTokenizer(v ollamaShow) (*qwenTokenizer, error) {
	i := v.ModelInfo
	if i["general.architecture"] != "qwen3" || i["tokenizer.ggml.model"] != "gpt2" || i["tokenizer.ggml.pre"] != "qwen2" || i["tokenizer.ggml.add_bos_token"] != false || fmt.Sprintf("%x", sha256.Sum256([]byte(v.Template))) != qwenTemplateHash {
		return nil, nil // Unsupported model retains the explicit conservative fallback.
	}
	tokens, a := i["tokenizer.ggml.tokens"].([]any)
	merges, b := i["tokenizer.ggml.merges"].([]any)
	kinds, c := i["tokenizer.ggml.token_type"].([]any)
	if !a || !b || !c || len(tokens) < 256 || len(merges) == 0 || len(kinds) != len(tokens) {
		return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
	}
	// Reverse GPT-2 byte-to-Unicode mapping. Merge keys are raw byte strings.
	decoder := map[rune]byte{}
	n := 0
	for b := 0; b < 256; b++ {
		r := rune(b)
		if !(b >= 33 && b <= 126 || b >= 161 && b <= 172 || b >= 174) {
			r = rune(256 + n)
			n++
		}
		decoder[r] = byte(b)
	}
	decode := func(s string) (string, bool) {
		var out []byte
		for _, r := range s {
			b, ok := decoder[r]
			if !ok {
				return "", false
			}
			out = append(out, b)
		}
		return string(out), true
	}
	t := &qwenTokenizer{ranks: map[tokenPair]int{}, pattern: regexp2.MustCompile(qwenPattern, 0)}
	t.pattern.MatchTimeout = time.Second
	vocab := map[string]bool{}
	for j, value := range tokens {
		s, ok := value.(string)
		if !ok || s == "" || vocab[s] {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
		vocab[s] = true
		kind, ok := kinds[j].(float64)
		if !ok {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
		if kind == 3 || kind == 4 {
			t.special = append(t.special, s)
		}
	}
	for r := range decoder {
		if !vocab[string(r)] {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
	}
	for rank, value := range merges {
		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
		left, right, ok := strings.Cut(s, " ")
		if !ok || !vocab[left] || !vocab[right] || !vocab[left+right] {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
		l, ok1 := decode(left)
		r, ok2 := decode(right)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
		key := tokenPair{l, r}
		if _, exists := t.ranks[key]; exists {
			return nil, fmt.Errorf("MODEL_TOKENIZER_UNVERIFIED")
		}
		t.ranks[key] = rank
	}
	return t, nil
}

type mergeCandidate struct{ rank, left, right, end int }
type mergeHeap []mergeCandidate

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if h[i].rank == h[j].rank {
		return h[i].left < h[j].left
	}
	return h[i].rank < h[j].rank
}
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(v any)   { *h = append(*h, v.(mergeCandidate)) }
func (h *mergeHeap) Pop() any     { old := *h; v := old[len(old)-1]; *h = old[:len(old)-1]; return v }

func (t *qwenTokenizer) piece(ctx context.Context, s string) (int, error) {
	if len(s) < 2 {
		return len(s), nil
	}
	next, prev, end := make([]int, len(s)), make([]int, len(s)), make([]int, len(s))
	alive := make([]bool, len(s))
	h := &mergeHeap{}
	for i := range next {
		next[i] = i + 1
		prev[i] = i - 1
		end[i] = i + 1
		alive[i] = true
	}
	add := func(i int) {
		if i < 0 || i >= len(s) || !alive[i] {
			return
		}
		j := next[i]
		if j >= len(s) {
			return
		}
		if rank, ok := t.ranks[tokenPair{s[i:end[i]], s[j:end[j]]}]; ok {
			heap.Push(h, mergeCandidate{rank, i, j, end[j]})
		}
	}
	for i := range next {
		add(i)
	}
	count := len(s)
	for h.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		m := heap.Pop(h).(mergeCandidate)
		i, j := m.left, m.right
		if !alive[i] || !alive[j] || next[i] != j || end[j] != m.end {
			continue
		}
		alive[j] = false
		end[i] = end[j]
		next[i] = next[j]
		if next[i] < len(s) {
			prev[next[i]] = i
		}
		count--
		add(prev[i])
		add(i)
	}
	return count, nil
}

func (t *qwenTokenizer) Count(ctx context.Context, text string) (int, error) {
	if !utf8.ValidString(text) {
		return 0, fmt.Errorf("MODEL_TOKENIZER_INVALID_UTF8")
	}
	count := 0
	for len(text) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		at, size := len(text), 0
		for _, s := range t.special {
			if p := strings.Index(text, s); p >= 0 && (p < at || p == at && len(s) > size) {
				at, size = p, len(s)
			}
		}
		plain := text[:at]
		m, err := t.pattern.FindStringMatch(plain)
		covered := 0
		for m != nil && err == nil {
			piece := m.String()
			covered += len(piece)
			n, e := t.piece(ctx, piece)
			if e != nil {
				return 0, e
			}
			count += n
			m, err = t.pattern.FindNextMatch(m)
		}
		if err != nil || covered != len(plain) {
			return 0, fmt.Errorf("MODEL_TOKENIZER_FAILED")
		}
		if size > 0 {
			count++
		}
		text = text[at+size:]
	}
	return count, nil
}
