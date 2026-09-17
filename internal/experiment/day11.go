package experiment

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"workshop-agent/internal/inventory"
	"workshop-agent/internal/llm"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/products"
	"workshop-agent/internal/workshops"
)

//go:embed report.html.tmpl
var reportTemplate string

type Result struct {
	Scenario        string         `json:"scenario"`
	Case            string         `json:"case"`
	Question        string         `json:"question"`
	Answer          string         `json:"answer"`
	Expected        string         `json:"expected"`
	CheckPassed     bool           `json:"check_passed"`
	Clarification   bool           `json:"clarification"`
	WrongAssumption bool           `json:"wrong_assumption"`
	Usage           *llm.Usage     `json:"provider_usage"`
	Context         memory.Context `json:"context"`
	Error           string         `json:"error,omitempty"`
}
type Summary struct {
	Case                                                                            string
	Count, Passed, Clarifications, WrongAssumptions, PromptTokens, CompletionTokens int
}
type Report struct {
	ManualNote              string `json:"ManualNote,omitempty"`
	RunID, CreatedAt, Model string
	Temperature             int
	MaxTokens               int
	Results                 []Result
	Summary                 []Summary
}
type scenario struct {
	Name, Question, Expected string
	Short                    []string
	Quantity                 float64
	Packaging                string
	Preference, Rule, Stale  bool
}

var scenarios = []scenario{
	{Name: "01 · Ссылка на цвет", Question: "А черного?", Expected: "4.2", Short: []string{"Какой остаток белого PETG?", "PETG White: 8.4 кг."}},
	{Name: "02 · Сохранение количества", Question: "Сколько кистей нужно на нашу текущую партию наборов?", Expected: "50", Quantity: 25, Short: []string{"Уточним план партии.", "Продолжаем расчёт."}},
	{Name: "03 · Временная упаковка", Question: "Какую коробку берём для этой партии?", Expected: "Box B", Quantity: 25, Packaging: "Box B", Short: []string{"Обсуждаем текущую партию.", "Продолжаем."}},
	{Name: "04 · Предпочтение после новой сессии", Question: "Подготовь отчет по текущим остаткам PETG.", Expected: "Итог", Preference: true},
	{Name: "05 · Domain DB против старого упоминания", Question: "Сколько белого PETG сейчас на складе?", Expected: "8.4", Stale: true},
	{Name: "06 · Правило мастерской", Question: "Какой входной контроль пластика нужен перед текущей партией?", Expected: "вес", Quantity: 25, Rule: true},
}

func RunDay11(ctx context.Context, client *llm.OpenRouterClient, root string) (string, error) {
	run := time.Now().UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join(root, run)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	report := Report{RunID: run, CreatedAt: time.Now().UTC().Format(time.RFC3339), Model: client.Model, Temperature: 0, MaxTokens: 512, Results: []Result{}}
	cases := []struct {
		Name    string
		Options memory.Options
	}{{"A", memory.Options{}}, {"B", memory.Options{Short: true}}, {"C", memory.Options{Short: true, Working: true}}, {"D", memory.All}}
	for i, scenario := range scenarios {
		db := filepath.Join(dir, fmt.Sprintf("scenario-%02d.db", i+1))
		ws := workshops.NewService(db)
		inv := inventory.NewService(db)
		prod := products.NewBOMService(db)
		sc, mem, domain, err := seedScenario(ws, inv, prod, scenario)
		if err != nil {
			ws.Close()
			inv.Close()
			prod.Close()
			return dir, err
		}
		for _, variant := range cases {
			built, err := (memory.AgentContextBuilder{Memory: mem}).Build(sc, scenario.Question, domain, variant.Options)
			if err != nil {
				return dir, err
			}
			// Every variant has identical identity/domain/current message. Only selected memory layers vary.
			built.Prompt += "\nAnswer the CURRENT user message in Russian. Give an actual answer, not a JSON command. Do not mention internal memory layers. If needed information is absent, ask one concise clarification."
			built.Trace.Tokens["TOTAL_PROMPT_ESTIMATE"] = memory.EstimateTokens(built.Prompt)
			result := Result{Scenario: scenario.Name, Case: variant.Name, Question: scenario.Question, Expected: scenario.Expected, Context: built}
			answer, usage, callErr := client.Complete(ctx, built.Prompt)
			result.Answer = answer
			result.Usage = usage
			if callErr != nil {
				result.Error = callErr.Error()
			} else {
				normalized := strings.ToLower(strings.ReplaceAll(answer, ",", "."))
				result.CheckPassed = strings.Contains(normalized, strings.ToLower(scenario.Expected))
				if scenario.Preference {
					result.CheckPassed = strings.HasPrefix(strings.TrimLeft(normalized, "#* \n"), "итог")
				}
				result.Clarification = strings.Contains(answer, "?") || strings.Contains(normalized, "уточнит")
				result.WrongAssumption = (i == 1 && strings.Contains(normalized, "40") && !strings.Contains(normalized, "50")) || (i == 2 && strings.Contains(normalized, "box a") && !strings.Contains(normalized, "box b")) || (i == 4 && strings.Contains(normalized, "10 кг") && !strings.Contains(normalized, "8.4"))
			}
			report.Results = append(report.Results, result)
			if err := writeReport(dir, &report); err != nil {
				return dir, err
			}
			fmt.Printf("Day11 %s %s: passed=%t, error=%t\n", scenario.Name, variant.Name, result.CheckPassed, callErr != nil)
			if callErr != nil {
				ws.Close()
				inv.Close()
				prod.Close()
				return dir, fmt.Errorf("LLM experiment incomplete: %w", callErr)
			}
		}
		ws.Close()
		inv.Close()
		prod.Close()
	}
	return filepath.Join(dir, "report.html"), writeReport(dir, &report)
}
func seedScenario(ws *workshops.Service, inv *inventory.Service, prod *products.Service, s scenario) (memory.Scope, *memory.Service, []memory.Item, error) {
	user, err := ws.UpsertUser(990001, "", "Учебный пользователь", "")
	if err != nil {
		return memory.Scope{}, nil, nil, err
	}
	w, err := ws.CreateOwnedWorkshop(user, "Учебная мастерская")
	if err != nil {
		return memory.Scope{}, nil, nil, err
	}
	for _, item := range []struct {
		Name, Unit string
		Qty        float64
	}{{"PETG White", "kg", 8.4}, {"PETG Black", "kg", 4.2}, {"Кисть", "pcs", 100}} {
		if _, err := inv.ForUser(user).CreateMaterial(w, item.Name, "raw", item.Unit, item.Qty, 0, "", 0, ""); err != nil {
			return memory.Scope{}, nil, nil, err
		}
	}
	product, err := prod.ForUser(user).CreateProduct(w, "Набор", "KIT", "kit", 0, 0, "")
	if err != nil {
		return memory.Scope{}, nil, nil, err
	}
	if err := prod.ForUser(user).SetBOMItem(w, product, "material", 3, 0, 2, "pcs", 0, ""); err != nil {
		return memory.Scope{}, nil, nil, err
	}
	mem := memory.New(ws.DB()).ForUser(user)
	sc, err := mem.EnsureSession(memory.Scope{UserID: user, WorkshopID: w}, 990001)
	if err != nil {
		return sc, nil, nil, err
	}
	for i, text := range s.Short {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := mem.AppendShortTerm(sc, role, text); err != nil {
			return sc, nil, nil, err
		}
	}
	if s.Quantity > 0 {
		state := memory.TaskState{ProductID: product, ProductName: "Набор", Quantity: s.Quantity, Parameters: map[string]string{}}
		if s.Packaging != "" {
			state.Parameters["packaging"] = s.Packaging
		}
		if _, err := mem.CreateWorkingMemory(sc, "production_plan", state); err != nil {
			return sc, nil, nil, err
		}
	}
	if s.Preference {
		if err := mem.SaveLongTermMemory(sc, memory.LongTerm{Type: "USER_PREFERENCE", ScopeType: "user", Key: "summary_first", Value: "true", Source: "experiment_explicit_write"}); err != nil {
			return sc, nil, nil, err
		}
		if err := mem.EndSession(sc); err != nil {
			return sc, nil, nil, err
		}
		sc, err = mem.EnsureSession(memory.Scope{UserID: user, WorkshopID: w}, 990001)
		if err != nil {
			return sc, nil, nil, err
		}
	}
	if s.Rule {
		if err := mem.SaveLongTermMemory(sc, memory.LongTerm{Type: "PROCESS_RULE", ScopeType: "process", Category: "quality_control", Key: "quality_control", Value: "При входном контроле обязательно проверяем вес каждой катушки.", Source: "experiment_explicit_write"}); err != nil {
			return sc, nil, nil, err
		}
	}
	if s.Stale {
		// Deliberately simulate an old imported record. The public write API rejects such stock facts.
		if _, err := ws.DB().Exec(`INSERT INTO long_term_memory(memory_type,category,scope_type,workshop_id,key,value_json,source_type,created_by_user_id) VALUES('WORKSHOP_KNOWLEDGE','history','workshop',?,'petg','"раньше было около 10 кг PETG"','legacy_fixture',?)`, w, user); err != nil {
			return sc, nil, nil, err
		}
	}
	if err := ws.SetWorkshopSettings(user, w, `{"packaging_default":"Box A"}`); err != nil {
		return sc, nil, nil, err
	}
	// Fixed, relevant domain snapshot shared by A/B/C/D; no real user data goes to the experiment provider.
	domain := []memory.Item{{Source: "materials", Key: "inventory", Content: `{"PETG White":{"current_stock":8.4,"unit":"kg"},"PETG Black":{"current_stock":4.2,"unit":"kg"},"Кисть":{"current_stock":100,"unit":"pcs"}}`}, {Source: "bom_items", Key: "product_1", Content: `{"product_id":1,"product_name":"Набор","Кисть_per_product":2}`}, {Source: "workshop_settings", Key: "packaging_default", Content: `{"packaging_default":"Box A"}`}}
	return sc, mem, domain, nil
}
func writeReport(dir string, r *Report) error {
	r.Summary = nil
	for _, name := range []string{"A", "B", "C", "D"} {
		s := Summary{Case: name}
		for _, v := range r.Results {
			if v.Case != name {
				continue
			}
			s.Count++
			if v.CheckPassed {
				s.Passed++
			}
			if v.Clarification {
				s.Clarifications++
			}
			if v.WrongAssumption {
				s.WrongAssumptions++
			}
			if v.Usage != nil {
				s.PromptTokens += v.Usage.PromptTokens
				s.CompletionTokens += v.Usage.CompletionTokens
			}
		}
		r.Summary = append(r.Summary, s)
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), raw, 0644); err != nil {
		return err
	}
	tmpl, err := template.New("report").Parse(reportTemplate)
	if err != nil {
		return err
	}
	file, err := os.Create(filepath.Join(dir, "report.html"))
	if err != nil {
		return err
	}
	defer file.Close()
	return tmpl.Execute(file, r)
}

// RenderExisting changes only presentation; recorded prompts, responses and scoring remain unchanged.
func RenderExisting(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		return err
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return err
	}
	return writeReport(dir, &report)
}
