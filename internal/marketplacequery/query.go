package marketplacequery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Scope struct{ UserID, WorkshopID int64 }

// Row is the boundary DTO. Identifiers retain their provider-specific meanings.
type Row struct {
	ProductID   int64  `json:"product_id"`
	VariantID   int64  `json:"variant_id"`
	SKU         int64  `json:"sku"`
	Barcode     string `json:"barcode"`
	WarehouseID int64  `json:"warehouse_id"`
	Name        string `json:"name"`
	Variant     string `json:"variant"`
	Quantity    int64  `json:"quantity"`
	Confirmed   bool   `json:"confirmed"`
	CapturedAt  string `json:"captured_at"`
}
type SourceResult struct {
	MCPCalls       int    `json:"mcp_calls"`
	Status         string `json:"status"`
	RetryNotBefore string `json:"retry_not_before,omitempty"`
	ConnectionID   int64  `json:"connection_id"`
	Rows           []Row  `json:"rows"`
}
type Item struct {
	Row
	Provider          Provider `json:"provider"`
	Source            Source   `json:"source"`
	ConnectionID      int64    `json:"connection_id"`
	InternalProductID int64    `json:"internal_product_id,omitempty"`
}
type Section struct {
	Tool           Tool   `json:"route"`
	Status         string `json:"status"`
	RetryNotBefore string `json:"retry_not_before,omitempty"`
	Items          []Item `json:"items"`
}
type Match struct {
	ProductID int64  `json:"internal_product_id"`
	Name      string `json:"name"`
	WB        int64  `json:"wb"`
	Ozon      int64  `json:"ozon"`
}
type Step struct {
	MCPCalls   int    `json:"mcp_calls"`
	Order      int    `json:"order"`
	Tool       Tool   `json:"route"`
	Status     string `json:"status"`
	Received   int    `json:"received"`
	Normalized int    `json:"normalized"`
	Matched    int    `json:"mapped"`
	Filtered   int    `json:"filtered"`
}
type Trace struct {
	ID         string                 `json:"trace_id"`
	Request    string                 `json:"request"`
	Intent     MarketplaceQueryIntent `json:"intent"`
	Plan       ExecutionPlan          `json:"plan"`
	Steps      []Step                 `json:"steps"`
	Status     string                 `json:"status"`
	DurationMS int64                  `json:"duration_ms"`
	Unmapped   int                    `json:"unmapped"`
}
type MarketplaceQueryResult struct {
	Status     string    `json:"status"`
	Sections   []Section `json:"sections"`
	Comparison []Match   `json:"comparison"`
	Unmapped   int       `json:"unmapped"`
	Trace      Trace     `json:"trace"`
}
type Caller interface {
	QueryTools() []Tool
	QueryCall(context.Context, Scope, Tool) (SourceResult, error)
}
type MarketplaceOrchestrator struct {
	Store *Repository
	MCP   Caller
}

func (o *MarketplaceOrchestrator) Execute(ctx context.Context, sc Scope, request string, i MarketplaceQueryIntent) (MarketplaceQueryResult, error) {
	out := MarketplaceQueryResult{}
	if e := o.Store.Authorize(sc); e != nil {
		return out, e
	}
	if o.Store.Sensitive != nil && o.Store.Sensitive(request) {
		return out, fmt.Errorf("sensitive input rejected")
	}
	if len([]rune(request)) > 2000 {
		return out, fmt.Errorf("query too long")
	}
	if e := i.Validate(); e != nil {
		return out, e
	}
	if i.Filters.UseThreshold {
		var n int64
		if o.Store.DB.QueryRow(`SELECT wb_low_stock_threshold FROM workshop_settings WHERE workshop_id=?`, sc.WorkshopID).Scan(&n) != nil {
			return out, fmt.Errorf("specify low stock threshold")
		}
		i.Filters.Quantity = &n
		i.Filters.UseThreshold = false
	}
	plan, e := (MarketplaceToolRouter{o.MCP.QueryTools()}).Route(i)
	if e != nil {
		return out, e
	}
	start := time.Now()
	var b [16]byte
	if _, e = rand.Read(b[:]); e != nil {
		return out, e
	}
	out.Trace = Trace{ID: hex.EncodeToString(b[:]), Request: request, Intent: i, Plan: plan, Steps: []Step{}}
	complete := 0
	bindings := map[int64]int64{}
	for _, t := range plan.Steps {
		id, rev := o.Store.Binding(sc, t.Provider)
		if id > 0 {
			bindings[id] = rev
		}
	}
	for n, t := range plan.Steps {
		if e = o.Store.Authorize(sc); e != nil {
			return MarketplaceQueryResult{}, e
		}
		v := SourceResult{Status: "UNAVAILABLE"}
		if t.Available {
			v, e = o.MCP.QueryCall(ctx, sc, t)
			if e != nil {
				v = SourceResult{Status: "FAILED"}
			}
		}
		if e = o.Store.Authorize(sc); e != nil {
			return MarketplaceQueryResult{}, e
		}
		step := Step{Order: n + 1, Tool: t, Status: v.Status, Received: len(v.Rows), MCPCalls: v.MCPCalls}
		section := Section{Tool: t, Status: v.Status, RetryNotBefore: v.RetryNotBefore, Items: []Item{}}
		if v.Status == "SUCCESS" {
			complete++
		}
		names, mapping, err := o.Store.Lookup(sc, t.Provider, v.ConnectionID)
		if err != nil {
			return MarketplaceQueryResult{}, err
		}
		variants, err := o.Store.Variants(sc, t.Provider, v.ConnectionID)
		if err != nil {
			return MarketplaceQueryResult{}, err
		}
		seen := map[string]bool{}
		for _, row := range v.Rows {
			rowKey := fmt.Sprintf("%s/%d", identity(t.Provider, row), row.WarehouseID)
			if t.Capability == Products {
				rowKey = fmt.Sprint(row.ProductID)
			}
			_, timeErr := time.Parse(time.RFC3339Nano, row.CapturedAt)
			if (row.ProductID <= 0 && !(t.Provider == Ozon && row.SKU > 0)) || row.Quantity < 0 || row.Quantity > 1000000000 || seen[rowKey] || timeErr != nil {
				if section.Status == "SUCCESS" {
					complete--
					section.Status = "PARTIAL"
					step.Status = "PARTIAL"
				}
				continue
			}
			seen[rowKey] = true
			if t.Provider == WB {
				row.Variant = variants[row.VariantID]
			}
			if v.Status != "SUCCESS" && v.Status != "PARTIAL" {
				row.Confirmed = false
			}
			key := identity(t.Provider, row)
			if row.Name == "" {
				row.Name = names[row.ProductID]
			}
			if row.Name == "" {
				id := row.ProductID
				if id == 0 {
					id = row.SKU
				}
				row.Name = fmt.Sprintf("Товар %d", id)
			}
			row.Name = safeText(row.Name, 120)
			row.Variant = safeText(row.Variant, 60)
			if o.Store.Sensitive != nil && (o.Store.Sensitive(row.Name) || o.Store.Sensitive(row.Variant)) {
				row.Name = "Товар"
				row.Variant = ""
			}
			x := Item{Row: row, Provider: t.Provider, Source: t.Source, ConnectionID: v.ConnectionID, InternalProductID: mapping[key]}
			step.Normalized++
			if !matchesProduct(row.Name, i.ProductName) {
				continue
			}
			if x.InternalProductID > 0 {
				step.Matched++
			} else {
				out.Unmapped++
			}
			if i.QueryType == Compare || i.QueryType == Products || accept(i, x) {
				section.Items = append(section.Items, x)
			}
		}
		sort.SliceStable(section.Items, func(a, b int) bool {
			if i.Sorting == "QUANTITY_ASC" && section.Items[a].Quantity != section.Items[b].Quantity {
				return section.Items[a].Quantity < section.Items[b].Quantity
			}
			return section.Items[a].Name < section.Items[b].Name
		})
		step.Filtered = len(section.Items)
		out.Trace.Steps = append(out.Trace.Steps, step)
		out.Sections = append(out.Sections, section)
	}
	out.Status = "PARTIAL_SUCCESS"
	if complete == len(plan.Steps) {
		out.Status = "SUCCESS"
	} else if complete == 0 {
		out.Status = "INCOMPLETE"
	}
	if i.QueryType == Compare {
		out.Comparison = compare(i, out.Sections)
	}
	out.Trace.Status = out.Status
	out.Trace.DurationMS = time.Since(start).Milliseconds()
	out.Trace.Unmapped = out.Unmapped
	if e = o.Store.Authorize(sc); e != nil {
		return MarketplaceQueryResult{}, e
	}
	for _, p := range []Provider{WB, Ozon} {
		id, rev := o.Store.Binding(sc, p)
		for oldID, oldRev := range bindings {
			expected := int64(1)
			if p == Ozon {
				expected = 2
			}
			if oldID == expected && (id != oldID || rev != oldRev) {
				return MarketplaceQueryResult{}, fmt.Errorf("connection changed during query")
			}
		}
	}
	raw, _ := json.Marshal(out.Trace)
	_, e = o.Store.DB.Exec(`INSERT INTO marketplace_query_traces(user_id,workshop_id,trace_json) VALUES(?,?,?)`, sc.UserID, sc.WorkshopID, string(raw))
	if e != nil {
		return MarketplaceQueryResult{}, e
	}
	return out, nil
}
func identity(p Provider, r Row) string {
	if p == Ozon {
		return fmt.Sprint(r.SKU)
	}
	return fmt.Sprintf("%d/%d/%s", r.ProductID, r.VariantID, r.Barcode)
}

// LLM supplies the base form; matching uses complete words, not substring IDs.
func matchesProduct(name, query string) bool {
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(s), "ё", "е"), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	if query == "" {
		return true
	}
	want := words(query)
	if len(want) == 0 {
		return false
	}
	have := words(name)
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w || productStem(h) == productStem(w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func productStem(word string) string {
	for _, ending := range []string{"ами", "ями", "ов", "ев", "ом", "ем", "ах", "ях", "ы", "и", "а", "я", "у", "ю", "е"} {
		if strings.HasSuffix(word, ending) && len([]rune(strings.TrimSuffix(word, ending))) >= 3 {
			return strings.TrimSuffix(word, ending)
		}
	}
	return word
}
func accept(i MarketplaceQueryIntent, x Item) bool {
	if !x.Confirmed {
		return i.Filters.Operator == "NONE"
	}
	if x.Quantity == 0 && !i.IncludeZeroStock {
		return false
	}
	if i.Filters.Quantity == nil {
		return true
	}
	n := *i.Filters.Quantity
	switch i.Filters.Operator {
	case "LT":
		return x.Quantity < n
	case "LE":
		return x.Quantity <= n
	case "EQ":
		return x.Quantity == n
	}
	return true
}
func compare(i MarketplaceQueryIntent, sections []Section) []Match {
	type value struct {
		sum  int64
		name string
	}
	sides := map[Provider]map[int64]value{WB: {}, Ozon: {}}
	for _, s := range sections {
		if s.Status != "SUCCESS" || s.Tool.Source != Seller {
			continue
		}
		for _, x := range s.Items {
			if x.InternalProductID == 0 || !x.Confirmed {
				continue
			}
			v := sides[x.Provider][x.InternalProductID]
			v.sum += x.Quantity
			v.name = x.Name
			sides[x.Provider][x.InternalProductID] = v
		}
	}
	out := []Match{}
	for id, a := range sides[WB] {
		b, ok := sides[Ozon][id]
		if !ok {
			continue
		}
		if i.ComparisonMode == "WB_AVAILABLE_OZON_ZERO" && !(a.sum > 0 && b.sum == 0) {
			continue
		}
		if i.ComparisonMode == "BOTH_ZERO" && !(a.sum == 0 && b.sum == 0) {
			continue
		}
		out = append(out, Match{id, a.name, a.sum, b.sum})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ProductID < out[b].ProductID })
	return out
}
func safeText(s string, n int) string {
	r := []rune(strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
func Render(r MarketplaceQueryResult) string {
	var b strings.Builder
	b.WriteString("Маркетплейсы · сохранённые/полученные данные\nИсточники показаны отдельно, количества между площадками не суммируются.\n")
	i := r.Trace.Intent
	if i.ProductName != "" {
		fmt.Fprintf(&b, "По названию: %s\n", safeText(i.ProductName, 120))
	}
	if i.Filters.Quantity != nil {
		op := map[string]string{"LT": "меньше", "LE": "не больше", "EQ": "равно"}[i.Filters.Operator]
		fmt.Fprintf(&b, "Условие: %s %d шт.\n", op, *i.Filters.Quantity)
	}
	if i.QueryType == Compare {
		for _, x := range r.Comparison {
			fmt.Fprintf(&b, "\n%s (внутренний товар %d)\nWB: %d; Ozon: %d\n", x.Name, x.ProductID, x.WB, x.Ozon)
		}
		if len(r.Comparison) == 0 {
			b.WriteString("Нет подтверждённых сопоставленных товаров для сравнения.\n")
		}
		fmt.Fprintf(&b, "Не сопоставлено позиций: %d. Неполные источники исключены из строгого сравнения.\n", r.Unmapped)
	}
	for _, s := range r.Sections {
		source := "Остатки продавца"
		if s.Tool.Source == Warehouse {
			source = "Остатки на складах площадки"
		}
		if s.Tool.Capability == Products {
			source = "Товары"
		}
		fmt.Fprintf(&b, "\n%s · %s\nСтатус: %s\n", s.Tool.Provider, source, s.Status)
		if s.RetryNotBefore != "" {
			fmt.Fprintf(&b, "Повтор после: %s\n", s.RetryNotBefore)
		}
		if i.QueryType == Compare {
			continue
		}
		if i.Total {
			var sum int64
			confirmed := 0
			for _, x := range s.Items {
				if x.Confirmed {
					sum += x.Quantity
					confirmed++
				}
			}
			if confirmed > 0 {
				label := "Подтверждённая сумма; полный остаток неизвестен"
				if s.Status == "SUCCESS" && confirmed == len(s.Items) {
					label = "Всего по этому источнику"
				}
				fmt.Fprintf(&b, "%s: %d шт.\n", label, sum)
			}
		}
		groups := []string{"Все позиции"}
		if i.Filters.SplitZero {
			groups = []string{"Нулевой остаток", "Низкий остаток, больше нуля"}
		}
		shown := 0
		for _, g := range groups {
			if len(groups) > 1 {
				b.WriteString(g + ":\n")
			}
			for _, x := range s.Items {
				if len(groups) > 1 && ((g == groups[0]) != (x.Confirmed && x.Quantity == 0)) {
					continue
				}
				if shown >= 40 {
					continue
				}
				shown++
				if i.QueryType == Products {
					fmt.Fprintf(&b, "• %s\n", x.Name)
					continue
				}
				state := fmt.Sprintf("%d шт.", x.Quantity)
				if !x.Confirmed {
					state = "остаток не подтверждён"
				}
				fmt.Fprintf(&b, "• %s %s — %s (склад %d; %s)\n", x.Name, x.Variant, state, x.WarehouseID, x.CapturedAt)
			}
		}
		if len(s.Items) == 0 {
			b.WriteString("Подтверждённых позиций по запросу нет; это не означает нулевые остатки.\n")
		}
		if len(s.Items) > shown {
			fmt.Fprintf(&b, "Показано %d из %d позиций.\n", shown, len(s.Items))
		}
	}
	fmt.Fprintf(&b, "\nРезультат: %s", r.Status)
	return b.String()
}
