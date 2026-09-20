package memory_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/workshops"
)

func setup(t *testing.T) (*workshops.Service, *memory.Service, memory.Scope) {
	t.Helper()
	ws := workshops.NewService(filepath.Join(t.TempDir(), "memory.db"))
	t.Cleanup(func() { ws.Close() })
	user, err := ws.UpsertUser(700001, "", "Owner", "")
	ok(t, err)
	w, err := ws.CreateOwnedWorkshop(user, "A")
	ok(t, err)
	m := memory.New(ws.DB()).ForUser(user)
	sc, err := m.EnsureSession(memory.Scope{UserID: user, WorkshopID: w}, 700001)
	ok(t, err)
	return ws, m, sc
}
func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func reject(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected rejection")
	}
}
func pref() memory.LongTerm {
	return memory.LongTerm{Type: "USER_PREFERENCE", ScopeType: "user", Key: "summary_first", Value: "true"}
}
func rule() memory.LongTerm {
	return memory.LongTerm{Type: "PROCESS_RULE", ScopeType: "process", Category: "quality_control", Key: "quality_control", Value: "При входном контроле проверяем вес каждой катушки."}
}

func TestThreeLayersUseSeparateStorage(t *testing.T) {
	ws, m, sc := setup(t)
	ok(t, m.AppendShortTerm(sc, "user", "Привет"))
	_, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{Quantity: 20})
	ok(t, err)
	ok(t, m.SaveLongTermMemory(sc, rule()))
	ok(t, m.SaveLongTermMemory(sc, pref()))
	for table, want := range map[string]int{"conversation_messages": 1, "working_memory": 1, "long_term_memory": 1, "user_preferences": 1} {
		var n int
		ok(t, ws.DB().QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		if n != want {
			t.Fatalf("%s=%d", table, n)
		}
	}
}
func TestShortTermRetentionAndSessionIsolation(t *testing.T) {
	_, m, sc := setup(t)
	m.MaxMessages = 3
	m.MaxTokens = 60
	for i := 0; i < 10; i++ {
		ok(t, m.AppendShortTerm(sc, "user", fmt.Sprintf("message %d", i)))
	}
	msgs, err := m.ShortTerm(sc)
	ok(t, err)
	if len(msgs) != 3 || msgs[0].Content != "message 7" {
		t.Fatal(msgs)
	}
	foreign := sc
	foreign.UserID++
	_, err = m.ShortTerm(foreign)
	reject(t, err)
	ok(t, m.EndSession(sc))
	fresh, err := m.EnsureSession(memory.Scope{UserID: sc.UserID, WorkshopID: sc.WorkshopID}, 700001)
	ok(t, err)
	msgs, err = m.ShortTerm(fresh)
	ok(t, err)
	if len(msgs) != 0 || fresh.SessionID == sc.SessionID {
		t.Fatal("history leaked into new session")
	}
}
func TestWorkingScopeUpdateAndCompletion(t *testing.T) {
	_, m, sc := setup(t)
	task, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{Quantity: 20})
	ok(t, err)
	sc.TaskID = task.ID
	ok(t, m.UpdateWorkingMemory(sc, memory.TaskState{Quantity: 25, Parameters: map[string]string{"packaging": "Box B"}}, "waiting_input"))
	task, err = m.ActiveWorking(sc)
	ok(t, err)
	if task.State.Quantity != 25 {
		t.Fatal(task)
	}
	wrong := sc
	wrong.TaskID = "other-task"
	reject(t, m.UpdateWorkingMemory(wrong, task.State, "active"))
	absent, err := m.ActiveWorking(wrong)
	ok(t, err)
	if absent != nil {
		t.Fatal("wrong task read")
	}
	reject(t, m.CompleteWorkingMemory(sc, false))
	ok(t, m.CompleteWorkingMemory(sc, true))
	task, err = m.ActiveWorking(sc)
	ok(t, err)
	if task != nil {
		t.Fatal("completed task active")
	}
	sc.TaskID = ""
	fresh, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{})
	ok(t, err)
	if fresh.State.Quantity != 0 {
		t.Fatal("old quantity reused")
	}
}
func TestPersonalPersistenceAndUserIsolation(t *testing.T) {
	ws, m, sc := setup(t)
	ok(t, m.SaveLongTermMemory(sc, pref()))
	ok(t, m.EndSession(sc))
	fresh, err := m.EnsureSession(memory.Scope{UserID: sc.UserID, WorkshopID: sc.WorkshopID}, 700001)
	ok(t, err)
	reopened := memory.New(ws.DB()).ForUser(sc.UserID)
	records, err := reopened.RelevantLongTerm(fresh, "отчет", "", 0)
	ok(t, err)
	if len(records) != 1 || records[0].Value != "true" {
		t.Fatal(records)
	}
	other, err := ws.UpsertUser(700002, "", "Other", "")
	ok(t, err)
	_, token, err := ws.CreateInvite(sc.UserID, sc.WorkshopID, auth.Viewer, 0, 0)
	ok(t, err)
	_, err = ws.AcceptInvite(other, token)
	ok(t, err)
	om := memory.New(ws.DB()).ForUser(other)
	osc, err := om.EnsureSession(memory.Scope{UserID: other, WorkshopID: sc.WorkshopID}, 700002)
	ok(t, err)
	records, err = om.RelevantLongTerm(osc, "отчет", "", 0)
	ok(t, err)
	if len(records) != 0 {
		t.Fatal("personal preference leaked")
	}
	_, err = m.RelevantLongTerm(osc, "отчет", "", 0)
	reject(t, err)
}
func TestSharedWorkshopKnowledgeAndPermissions(t *testing.T) {
	ws, m, sc := setup(t)
	ok(t, m.SaveLongTermMemory(sc, rule()))
	other, err := ws.UpsertUser(700002, "", "Worker", "")
	ok(t, err)
	_, token, err := ws.CreateInvite(sc.UserID, sc.WorkshopID, auth.Employee, 0, 0)
	ok(t, err)
	_, err = ws.AcceptInvite(other, token)
	ok(t, err)
	om := memory.New(ws.DB()).ForUser(other)
	osc := memory.Scope{UserID: other, WorkshopID: sc.WorkshopID}
	records, err := om.RelevantLongTerm(osc, "входной контроль", "production", 0)
	ok(t, err)
	if len(records) != 1 {
		t.Fatal(records)
	}
	reject(t, om.SaveLongTermMemory(osc, rule()))
	reject(t, om.DeactivateLongTermMemory(osc, records[0].ID))
	ok(t, ws.ChangeMemberStatus(sc.UserID, sc.WorkshopID, other, "disabled"))
	_, err = om.RelevantLongTerm(osc, "контроль", "production", 0)
	reject(t, err)
}
func TestMultiWorkshopIsolationAndActiveValidation(t *testing.T) {
	ws, m, sc := setup(t)
	ok(t, m.SaveLongTermMemory(sc, rule()))
	second, err := ws.CreateOwnedWorkshop(sc.UserID, "B")
	ok(t, err)
	_, err = m.RelevantLongTerm(sc, "контроль", "production", 0)
	if !errors.Is(err, memory.ErrScope) {
		t.Fatal("stale workshop accepted", err)
	}
	bsc := memory.Scope{UserID: sc.UserID, WorkshopID: second}
	records, err := m.RelevantLongTerm(bsc, "контроль", "production", 0)
	ok(t, err)
	if len(records) != 0 {
		t.Fatal("workshop A rule leaked into B")
	}
	bsc.SessionID = sc.SessionID
	_, err = m.ShortTerm(bsc)
	reject(t, err)
}
func TestRouterDoesNotPromoteTaskOrDomainFacts(t *testing.T) {
	_, m, sc := setup(t)
	r := memory.MemoryRouter{}
	task := &memory.Task{Type: "assembly"}
	cases := map[string]memory.Target{"Для этой партии используй Box B": memory.Working, "Запомни, что мне удобнее, когда итог идет первым": memory.Personal, "Теперь всегда клади в этот комплект 3 кисти": memory.Domain, "На складе 12 кг PETG": memory.Domain, "Сегодня произвели 40 изделий": memory.Domain, "А черного?": memory.Short, "token=secret": memory.Discard}
	for text, want := range cases {
		c := r.Route(text, sc, task)
		if c.Target != want || c.Reason == "" {
			t.Fatalf("%s: %+v", text, c)
		}
	}
	question := r.Route("Какую коробку берём для этой партии?", sc, task)
	if question.Target == memory.Working {
		t.Fatal("question was treated as a task mutation")
	}
	correction := r.Route("Сделать 25 шт.", sc, task)
	if correction.Target != memory.Working || correction.Quantity != 25 {
		t.Fatal("task correction misrouted", correction)
	}
	reject(t, m.SaveLongTermMemory(sc, memory.LongTerm{Type: "WORKSHOP_KNOWLEDGE", ScopeType: "workshop", Key: "stock", Value: "На складе 12 кг PETG"}))
}
func TestRelevantRetrievalAndProvenance(t *testing.T) {
	_, m, sc := setup(t)
	for i := 0; i < 40; i++ {
		ok(t, m.SaveLongTermMemory(sc, memory.LongTerm{Type: "WORKSHOP_KNOWLEDGE", ScopeType: "workshop", Category: "unrelated", Key: fmt.Sprintf("unrelated_%d", i), Value: "Other process"}))
	}
	ok(t, m.SaveLongTermMemory(sc, rule()))
	ok(t, m.AppendShortTerm(sc, "user", "Контроль PETG"))
	_, err := m.CreateWorkingMemory(sc, "production", memory.TaskState{Quantity: 25})
	ok(t, err)
	built, err := (memory.AgentContextBuilder{Memory: m}).Build(sc, "Входной контроль", []memory.Item{{Source: "materials", Key: "1", Content: `{"stock":8.4}`}}, memory.All)
	ok(t, err)
	if len(built.Long) != 1 || strings.Contains(built.Prompt, "unrelated_") {
		t.Fatal("irrelevant memory selected")
	}
	for _, layer := range []string{"IDENTITY", "SHORT_TERM", "WORKING", "LONG_TERM", "DOMAIN", "CURRENT"} {
		if !strings.Contains(built.Prompt, "["+layer+" ") {
			t.Fatal("missing provenance", layer)
		}
	}
	ok(t, m.SaveTrace(sc, built.Trace))
	trace, err := m.LastTrace(sc)
	ok(t, err)
	if len(trace.Items) != len(built.Trace.Items) {
		t.Fatal("trace mismatch")
	}
}
func TestPersistentVersionAuditAndDeactivation(t *testing.T) {
	ws, m, sc := setup(t)
	r := rule()
	ok(t, m.SaveLongTermMemory(sc, r))
	r.Value = "Контроль веса и этикетки каждой катушки."
	ok(t, m.UpdateLongTermMemory(sc, r))
	records, err := m.RelevantLongTerm(sc, "контроль", "production", 0)
	ok(t, err)
	if records[0].Version != 2 {
		t.Fatal("version did not advance")
	}
	ok(t, m.DeactivateLongTermMemory(sc, records[0].ID))
	records, err = m.RelevantLongTerm(sc, "контроль", "production", 0)
	ok(t, err)
	if len(records) != 0 {
		t.Fatal("inactive memory retrieved")
	}
	var count int
	ok(t, ws.DB().QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE event_type IN ('MEMORY_LONG_TERM_SAVED','MEMORY_DEACTIVATED')`).Scan(&count))
	if count != 3 {
		t.Fatal("persistent mutation audit missing")
	}
}
func TestShortTermTokenBudget(t *testing.T) {
	_, m, sc := setup(t)
	m.MaxMessages = 20
	m.MaxTokens = 20
	for i := 0; i < 5; i++ {
		ok(t, m.AppendShortTerm(sc, "user", strings.Repeat("я", 20)))
	}
	msgs, err := m.ShortTerm(sc)
	ok(t, err)
	total := 0
	for _, msg := range msgs {
		total += msg.Tokens
	}
	if total > 20 {
		t.Fatal("budget exceeded")
	}
}

func TestLongTermDatabaseReopen(t *testing.T) {
	ws, m, sc := setup(t)
	ok(t, m.SaveLongTermMemory(sc, pref()))
	var seq int
	var name, path string
	ok(t, ws.DB().QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path))
	ok(t, ws.Close())
	reopened := workshops.NewService(path)
	defer reopened.Close()
	records, err := memory.New(reopened.DB()).ForUser(sc.UserID).RelevantLongTerm(sc, "отчет", "", 0)
	ok(t, err)
	if len(records) != 1 {
		t.Fatal("preference did not survive database reopen")
	}
}
func TestProductMemoryScope(t *testing.T) {
	ws, m, sc := setup(t)
	res, err := ws.DB().Exec(`INSERT INTO products(workshop_id,name,product_type) VALUES(?,'Product','product')`, sc.WorkshopID)
	ok(t, err)
	product, _ := res.LastInsertId()
	record := memory.LongTerm{Type: "PRODUCT_KNOWLEDGE", ScopeType: "product", Category: "packaging", Key: "cooling", EntityID: product, Value: "Проверять поверхность после остывания."}
	ok(t, m.SaveLongTermMemory(sc, record))
	relevant, err := m.RelevantLongTerm(sc, "осмотр", "", product)
	ok(t, err)
	if len(relevant) != 1 {
		t.Fatal("product context missing")
	}
	unrelated, err := m.RelevantLongTerm(sc, "осмотр", "", product+1)
	ok(t, err)
	if len(unrelated) != 0 {
		t.Fatal("other product memory leaked")
	}
	record.EntityID = 999
	reject(t, m.SaveLongTermMemory(sc, record))
}
func TestCandidateProposalSessionBinding(t *testing.T) {
	_, m, sc := setup(t)
	c := (memory.MemoryRouter{}).Route("Запомни, что мне удобнее краткие отчеты", sc, nil)
	ok(t, m.Propose(sc, 700001, c))
	ok(t, m.EndSession(sc))
	fresh, err := m.EnsureSession(memory.Scope{UserID: sc.UserID, WorkshopID: sc.WorkshopID}, 700001)
	ok(t, err)
	_, err = m.TakeProposal(fresh, 700001)
	reject(t, err)
	records, err := m.RelevantLongTerm(fresh, "отчет", "", 0)
	ok(t, err)
	if len(records) != 0 {
		t.Fatal("stale proposal persisted")
	}
}
func TestWorkingUserAndWorkshopIsolation(t *testing.T) {
	ws, m, sc := setup(t)
	_, err := m.CreateWorkingMemory(sc, "assembly", memory.TaskState{Quantity: 25})
	ok(t, err)
	other := sc
	other.UserID++
	_, err = m.ActiveWorking(other)
	reject(t, err)
	b, err := ws.CreateOwnedWorkshop(sc.UserID, "B")
	ok(t, err)
	other = sc
	other.WorkshopID = b
	task, err := m.ActiveWorking(other)
	ok(t, err)
	if task != nil {
		t.Fatal("workshop task leak")
	}
}
func TestShortTermClippingAndNoPromotion(t *testing.T) {
	ws, m, sc := setup(t)
	m.MaxTokens = 10
	ok(t, m.AppendShortTerm(sc, "user", strings.Repeat("длинный диалог ", 100)))
	msgs, err := m.ShortTerm(sc)
	ok(t, err)
	if len(msgs) != 1 || msgs[0].Tokens > 10 {
		t.Fatal("unbounded history")
	}
	var n int
	ok(t, ws.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM working_memory)+(SELECT COUNT(*) FROM long_term_memory)+(SELECT COUNT(*) FROM user_preferences)`).Scan(&n))
	if n != 1 { // The default user profile exists before any short-term message.
		t.Fatal("short-term automatically promoted")
	}
}
