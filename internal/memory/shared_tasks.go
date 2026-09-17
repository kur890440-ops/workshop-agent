package memory

import (
	"database/sql"
	"errors"
	"workshop-agent/internal/auth"
)

func (s *Service) legacyTaskPermission(tx *sql.Tx, sc Scope) error {
	t, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
	if err != nil {
		return err
	}
	if shared(t) {
		return TaskPermission(tx, sc.UserID, sc.WorkshopID, t)
	}
	if t.UserID != sc.UserID {
		return ErrScope
	}
	return nil
}

func shared(t *Task) bool { return t.Type == "assembly" || t.Type == "production_plan" }
func businessParameter(key string) bool {
	return key == "packaging" || key == "material" || key == "orders" || key == "per_order"
}
func preservePrivateTaskState(tx *sql.Tx, sc Scope, state *TaskState) error {
	t, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE task_id=? AND workshop_id=?", sc.TaskID, sc.WorkshopID))
	if err != nil {
		return err
	}
	if !shared(t) {
		return nil
	}
	for k, v := range t.State.Parameters {
		if !businessParameter(k) {
			if state.Parameters == nil {
				state.Parameters = map[string]string{}
			}
			if _, exists := state.Parameters[k]; !exists {
				state.Parameters[k] = v
			}
		}
	}
	if state.OpenQuestions == nil {
		state.OpenQuestions = t.State.OpenQuestions
	}
	return nil
}
func publicTask(t *Task) *Task {
	if t != nil && shared(t) {
		c := *t
		c.State.Parameters = nil
		// Only established business fields are shared; arbitrary notes and
		// presentation overrides remain stored but are not exposed.
		for _, key := range []string{"packaging", "material", "orders", "per_order"} {
			if value, ok := t.State.Parameters[key]; ok {
				if c.State.Parameters == nil {
					c.State.Parameters = map[string]string{}
				}
				c.State.Parameters[key] = value
			}
		}
		c.State.OpenQuestions = nil
		return &c
	}
	return t
}
func TaskPermission(q auth.Querier, actor, workshop int64, t *Task) error {
	if actor == t.AssignedToUserID {
		return auth.Require(q, actor, workshop, auth.TasksExecute)
	}
	return auth.Require(q, actor, workshop, auth.TasksManage)
}
func assignmentAllowed(q auth.Querier, sc Scope, user int64) error {
	if err := auth.Require(q, user, sc.WorkshopID, auth.TasksExecute); err != nil {
		return err
	}
	if user != sc.UserID {
		return auth.Require(q, sc.UserID, sc.WorkshopID, auth.TasksAssign)
	}
	return nil
}
func (s *Service) WorkshopTasks(sc Scope, mine bool) ([]*Task, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	if err := auth.Require(s.DB, sc.UserID, sc.WorkshopID, auth.TasksRead); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query("SELECT "+taskColumns+" FROM working_memory WHERE workshop_id=? AND task_type IN ('assembly','production_plan') AND status IN ('active','waiting_input','paused') AND (?=0 OR assigned_to_user_id=?) ORDER BY id DESC", sc.WorkshopID, mine, sc.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, publicTask(t))
	}
	return out, rows.Err()
}

type Executor struct {
	ID         int64
	Name, Role string
}

func (s *Service) Executors(sc Scope) ([]Executor, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	if err := auth.Require(s.DB, sc.UserID, sc.WorkshopID, auth.TasksRead); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT u.id,COALESCE(NULLIF(u.display_name,''),'User'),m.role FROM users u JOIN workshop_members m ON m.user_id=u.id WHERE m.workshop_id=? AND m.status='active' AND m.is_active=1 AND u.status='active' AND u.is_active=1 AND m.role IN ('OWNER','ADMIN','EMPLOYEE') ORDER BY u.id`, sc.WorkshopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Executor{}
	for rows.Next() {
		var e Executor
		if err = rows.Scan(&e.ID, &e.Name, &e.Role); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Service) AssignTask(sc Scope, assignee int64, version int) (*Task, error) {
	err := s.tx(sc, func(tx *sql.Tx) error {
		if err := auth.Require(tx, sc.UserID, sc.WorkshopID, auth.TasksAssign); err != nil {
			return err
		}
		t, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM working_memory WHERE workshop_id=? AND task_id=?", sc.WorkshopID, sc.TaskID))
		if err != nil {
			return err
		}
		if !shared(t) {
			return auth.ErrDenied
		}
		if t.Version != version {
			return ErrTaskChanged
		}
		if t.Status != "active" && t.Status != "waiting_input" && t.Status != "paused" {
			return ErrTransition
		}
		if t.Status != "paused" && t.Phase != "planning" {
			return errors.New("Сначала явно поставьте выполняемую задачу на паузу.")
		}
		if err = assignmentAllowed(tx, sc, assignee); err != nil {
			return err
		}
		var n int
		if err = tx.QueryRow("SELECT COUNT(*) FROM working_memory WHERE workshop_id=? AND COALESCE(assigned_to_user_id,user_id)=? AND status IN ('active','waiting_input') AND task_id<>?", sc.WorkshopID, assignee, t.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrForeground
		}
		before := *t
		t.AssignedToUserID = assignee
		t.Version++
		if _, err = tx.Exec("UPDATE working_memory SET assigned_to_user_id=?,version=?,updated_at=CURRENT_TIMESTAMP WHERE task_id=? AND workshop_id=?", assignee, t.Version, t.ID, sc.WorkshopID); err != nil {
			return err
		}
		return taskEvent(tx, sc, before, *t, "assigned")
	})
	if err != nil {
		return nil, err
	}
	return s.Task(sc)
}
