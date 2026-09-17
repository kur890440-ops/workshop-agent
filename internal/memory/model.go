// Package memory separates dialog, task state, persistent knowledge and domain truth.
package memory

import (
	"encoding/json"
	"strings"
	"workshop-agent/internal/personalization"
)

type Scope struct {
	UserID     int64  `json:"user_id"`
	WorkshopID int64  `json:"workshop_id"`
	SessionID  int64  `json:"session_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
}
type Message struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Tokens  int    `json:"tokens_estimate"`
}
type TaskState struct {
	ProducedQuantity *float64          `json:"produced_quantity,omitempty"`
	ProductID        int64             `json:"product_id,omitempty"`
	ProductName      string            `json:"product_name,omitempty"`
	Quantity         float64           `json:"quantity"`
	Parameters       map[string]string `json:"parameters,omitempty"`
	OpenQuestions    []string          `json:"open_questions,omitempty"`
}
type Task struct {
	CreatedByUserID, AssignedToUserID                      int64
	UserID, WorkshopID                                     int64
	Phase, CurrentStep, ExpectedAction, ExpectedActionType string
	CreatedAt, UpdatedAt                                   string
	StartedAt, PausedAt, CompletedAt                       *string
	Version, FSMVersion                                    int
	ID                                                     string    `json:"task_id"`
	Type                                                   string    `json:"task_type"`
	State                                                  TaskState `json:"state"`
	Status                                                 string    `json:"status"`
}
type LongTerm struct {
	ID        int64  `json:"id"`
	Type      string `json:"memory_type"`
	Category  string `json:"category"`
	ScopeType string `json:"scope_type"`
	EntityID  int64  `json:"entity_id,omitempty"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	Version   int    `json:"version"`
}
type Target string

const (
	Short    Target = "SHORT_TERM"
	Working  Target = "WORKING"
	Personal Target = "LONG_TERM_PERSONAL"
	Workshop Target = "LONG_TERM_WORKSHOP"
	Domain   Target = "DOMAIN_OPERATION"
	Discard  Target = "DO_NOT_STORE"
)

type Candidate struct {
	Content              string  `json:"content"`
	Target               Target  `json:"target"`
	Category             string  `json:"category"`
	Scope                Scope   `json:"scope"`
	Confidence           float64 `json:"confidence"`
	Reason               string  `json:"reason"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
	Key                  string  `json:"key,omitempty"`
	Value                string  `json:"value,omitempty"`
	Quantity             float64 `json:"quantity,omitempty"`
	ProductID            int64   `json:"product_id,omitempty"`
	MaterialID           int64   `json:"material_id,omitempty"`
}
type Options struct{ Short, Working, Long bool }

var All = Options{true, true, true}

type Item struct {
	Layer   string `json:"layer"`
	Source  string `json:"source"`
	Key     string `json:"key"`
	Content string `json:"content"`
	Tokens  int    `json:"tokens_estimate"`
}
type Trace struct {
	Scope    Scope          `json:"scope"`
	Items    []Item         `json:"items"`
	Tokens   map[string]int `json:"tokens_estimate"`
	Warnings []string       `json:"warnings,omitempty"`
	Router   *Candidate     `json:"router,omitempty"`
}
type Context struct {
	Profile personalization.Resolution `json:"personalization"`
	Prompt  string                     `json:"prompt"`
	Trace   Trace                      `json:"trace"`
	Short   []Message                  `json:"short"`
	Working *Task                      `json:"working,omitempty"`
	Long    []LongTerm                 `json:"long"`
}

// EstimateTokens is a deterministic byte-based estimate, NOT provider token accounting.
func EstimateTokens(s string) int { return (len([]byte(s)) + 2) / 3 }
func compact(v any) string        { b, _ := json.Marshal(v); return string(b) }
func containsAny(s string, terms ...string) bool {
	for _, t := range terms {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}
