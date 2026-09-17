package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"workshop-agent/internal/llm"
)

// RunSemantic evaluates interpretation only. It never opens the working database
// and cannot perform inventory mutations or call Telegram.
func RunSemantic(ctx context.Context, client *llm.OpenRouterClient) (string, error) {
	type sample struct {
		Text, Action, Kind, Mode string
		Position                 int
		Amount                   *float64
		Context                  string
	}
	n := func(v float64) *float64 { return &v }
	catalog := `"materials":[{"id":11,"name":"Гипс","unit":"kg"},{"id":12,"name":"Кисточки","unit":"pcs"},{"id":13,"name":"Коробки","unit":"pcs"}]`
	listed := `{"user_id":1,"workshop_id":1,"session_id":1,"last_list_type":"material","list_ids":[11,12,13],"last_material_id":12,` + catalog + `}`
	noList := `{"user_id":1,"workshop_id":1,"session_id":2,"last_list_type":"","list_ids":[],"last_material_id":0,` + catalog + `}`
	samples := []sample{
		{Text: "сколько второго?", Action: "get_material_stock", Kind: "list_position", Position: 2},
		{Text: "какой остаток 2", Action: "get_material_stock", Kind: "list_position", Position: 2},
		{Text: "остаток второго материала", Action: "get_material_stock", Kind: "list_position", Position: 2},
		{Text: "сколько осталось кисточек?", Action: "get_material_stock", Kind: "name"},
		{Text: "а кисточек сколько?", Action: "get_material_stock", Kind: "name"},
		{Text: "покажи остаток позиции 2", Action: "get_material_stock", Kind: "list_position", Position: 2},
		{Text: "сколько по второму пункту?", Action: "get_material_stock", Kind: "list_position", Position: 2},
		{Text: "а третьего?", Action: "get_material_stock", Kind: "list_position", Position: 3},
		{Text: "а его минимум?", Action: "get_material_minimum", Kind: "last"},
		{Text: "сколько кисточик осталось?", Action: "get_material_stock", Kind: "name"},
		{Text: "какой остаток 2", Action: "get_material_stock", Kind: "list_position", Position: 2, Context: noList},
		{Text: "добавь 2", Action: "clarification", Context: noList},
		{Text: "установи остаток гипса 10 кг", Action: "change_material_stock", Kind: "name", Mode: "absolute", Amount: n(10)},
		{Text: "добавь 2 кг гипса на склад", Action: "change_material_stock", Kind: "name", Mode: "increase", Amount: n(2)},
		{Text: "спиши 0,5 кг гипса", Action: "change_material_stock", Kind: "name", Mode: "decrease", Amount: n(.5)},
		{Text: "что нужно закупить?", Action: "get_purchase_needs"},
		{Text: "покажи текущую задачу", Action: "get_task"},
		{Text: "какой остаток 2", Action: "clarification", Context: `{"last_list_type":"product","list_ids":[21,22],` + catalog + `}`},
	}
	type result struct {
		Text    string
		Command *llm.StructuredCommand
		Usage   *llm.Usage
		Passed  bool
		Error   string
	}
	report := struct {
		Model                 string
		Started               time.Time
		Passed, Total, Tokens int
		Results               []result
	}{Model: client.Model, Started: time.Now().UTC(), Total: len(samples)}
	dir := filepath.Join("reports", "semantic", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "results.json")
	for i, s := range samples {
		contextJSON := s.Context
		if contextJSON == "" {
			contextJSON = listed
		}
		cmd, usage, err := client.Interpret(ctx, contextJSON, s.Text)
		r := result{Text: s.Text, Command: cmd, Usage: usage}
		if err != nil {
			r.Error = "provider_or_schema_error"
		} else {
			r.Passed = cmd.Action == s.Action
			if s.Kind != "" {
				r.Passed = r.Passed && cmd.Reference != nil
				if cmd.Reference != nil {
					r.Passed = r.Passed && cmd.Reference.Kind == s.Kind
					if s.Position > 0 {
						r.Passed = r.Passed && cmd.Reference.Position == s.Position
					}
				}
			}
			if s.Amount != nil {
				r.Passed = r.Passed && cmd.Amount != nil && cmd.QuantityMode == s.Mode
				if cmd.Amount != nil {
					r.Passed = r.Passed && *cmd.Amount == *s.Amount
				}
			}
		}
		if r.Passed {
			report.Passed++
		}
		if usage != nil {
			report.Tokens += usage.TotalTokens
		}
		report.Results = append(report.Results, r)
		raw, _ := json.MarshalIndent(report, "", "  ")
		if e := os.WriteFile(path, raw, 0644); e != nil {
			return path, e
		}
		fmt.Printf("semantic %d/%d passed=%v\n", i+1, len(samples), r.Passed)
	}
	fmt.Printf("passed=%d/%d tokens=%d\n", report.Passed, report.Total, report.Tokens)
	return path, nil
}
