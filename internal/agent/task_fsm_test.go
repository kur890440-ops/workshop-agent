package agent

import (
	"strings"
	"testing"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/personalization"
)

func TestTaskChoiceAndPersonalization(t *testing.T) {
	a, u, w := day11(t)
	run := func(text string) string {
		t.Helper()
		handled, answer, err := a.TaskMessage(u, w, text)
		if !handled || err != nil {
			t.Fatal(text, handled, err)
		}
		return answer
	}
	run("/task new")
	run("/task pause")
	run("/task new")
	run("/task pause")
	if answer := run("Продолжим задачу"); !strings.Contains(answer, "Выберите задачу") {
		t.Fatal(answer)
	}
	sc := memory.Scope{UserID: u, WorkshopID: w}
	tasks, err := a.Memory.ForUser(u).Tasks(sc)
	if err != nil || len(tasks) != 2 {
		t.Fatal(tasks, err)
	}
	run("/task resume " + tasks[0].ID)
	brief := run("/task")
	if err = personalization.New(a.WS.DB()).ForUser(u).UpdatePreference("response_style", "detailed", "test"); err != nil {
		t.Fatal(err)
	}
	detailed := run("/task")
	if brief == detailed || !strings.Contains(detailed, "version=") {
		t.Fatal(brief, detailed)
	}
	after, err := a.Memory.ForUser(u).ActiveWorking(sc)
	if err != nil || after.Phase != tasks[0].Phase || after.CurrentStep != tasks[0].CurrentStep {
		t.Fatal(after, err)
	}
	if _, _, err = a.TaskMessage(u, w, "/task complete"); err == nil {
		t.Fatal("profile bypassed FSM")
	}
}
