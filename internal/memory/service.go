package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"workshop-agent/internal/auth"
	"workshop-agent/internal/personalization"
)

var ErrScope = errors.New("Контекст памяти недоступен для этого пользователя или мастерской.")
var ErrNoTask = errors.New("Нет активной задачи. Сначала начните новую задачу.")
var ErrDomain = errors.New("Это бизнес-данные: используйте склад, BOM или производство, а не долговременную память.")

type Service struct {
	DB                     *sql.DB
	UserID                 int64
	MaxMessages, MaxTokens int
}

func New(db *sql.DB) *Service                  { return &Service{DB: db, MaxMessages: 20, MaxTokens: 1600} }
func (s *Service) ForUser(user int64) *Service { copy := *s; copy.UserID = user; return &copy }
func (s *Service) authorize(q auth.Querier, sc Scope) error {
	if sc.UserID != s.UserID || s.UserID <= 0 {
		return ErrScope
	}
	if err := auth.Require(q, sc.UserID, sc.WorkshopID, auth.WorkshopRead); err != nil {
		return err
	}
	var active sql.NullInt64
	if err := q.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, sc.UserID).Scan(&active); err != nil {
		return ErrScope
	}
	if !active.Valid || active.Int64 != sc.WorkshopID {
		return ErrScope
	}
	return nil
}
func (s *Service) tx(sc Scope, fn func(*sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE schema_migrations SET name=name WHERE number=101`); err != nil {
		return err
	}
	if err := s.authorize(tx, sc); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func memoryAudit(tx *sql.Tx, sc Scope, event, key string, before, after any, source string) error {
	metadata := compact(map[string]any{"scope": sc, "key": key, "before": before, "after": after, "source": source})
	_, err := tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,event_type,metadata_json,target_user_id) VALUES(?,'memory',0,?,'',?,?,'','','',?,?,?)`, sc.WorkshopID, sc.UserID, event, key, event, metadata, sc.UserID)
	return err
}
func (s *Service) EnsureSession(sc Scope, chatID int64) (Scope, error) {
	err := s.tx(sc, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT id FROM conversation_sessions WHERE user_id=? AND workshop_id=? AND telegram_chat_id=? AND status='active'`, sc.UserID, sc.WorkshopID, chatID).Scan(&sc.SessionID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		r, err := tx.Exec(`INSERT INTO conversation_sessions(user_id,workshop_id,telegram_chat_id,last_context,pending_action,status) VALUES(?,?,?,'','','active')`, sc.UserID, sc.WorkshopID, chatID)
		if err != nil {
			return err
		}
		sc.SessionID, err = r.LastInsertId()
		return err
	})
	return sc, err
}
func session(q auth.Querier, sc Scope) error {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM conversation_sessions WHERE id=? AND user_id=? AND workshop_id=? AND status='active'`, sc.SessionID, sc.UserID, sc.WorkshopID).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrScope
	}
	return nil
}
func (s *Service) EndSession(sc Scope) error {
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := session(tx, sc); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE conversation_sessions SET status='closed',updated_at=CURRENT_TIMESTAMP WHERE id=?`, sc.SessionID)
		return err
	})
}
func (s *Service) limits() (int, int) {
	n, t := s.MaxMessages, s.MaxTokens
	if n <= 0 {
		n = 20
	}
	if t <= 0 {
		t = 1600
	}
	return n, t
}
func (s *Service) AppendShortTerm(sc Scope, role, content string) error {
	if role != "user" && role != "assistant" {
		return errors.New("invalid message role")
	}
	n, budget := s.limits()
	// Oversized turns are clipped at rune boundaries; current input is still separately available.
	var clipped strings.Builder
	for _, r := range content {
		piece := string(r)
		if clipped.Len()+len(piece) > budget*3 {
			break
		}
		clipped.WriteString(piece)
	}
	content = clipped.String()
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := session(tx, sc); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO conversation_messages(user_id,workshop_id,session_id,role,content,token_count) VALUES(?,?,?,?,?,?)`, sc.UserID, sc.WorkshopID, sc.SessionID, role, content, EstimateTokens(content)); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id,token_count FROM conversation_messages WHERE user_id=? AND workshop_id=? AND session_id=? ORDER BY id DESC LIMIT ?`, sc.UserID, sc.WorkshopID, sc.SessionID, n)
		if err != nil {
			return err
		}
		var oldest int64
		total := 0
		for rows.Next() {
			var id int64
			var tokens int
			if err := rows.Scan(&id, &tokens); err != nil {
				rows.Close()
				return err
			}
			if total+tokens > budget {
				break
			}
			total += tokens
			oldest = id
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM conversation_messages WHERE user_id=? AND workshop_id=? AND session_id=? AND id<?`, sc.UserID, sc.WorkshopID, sc.SessionID, oldest); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE conversation_sessions SET updated_at=CURRENT_TIMESTAMP WHERE id=?`, sc.SessionID)
		return err
	})
}
func (s *Service) ShortTerm(sc Scope) ([]Message, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	if err := session(s.DB, sc); err != nil {
		return nil, err
	}
	n, budget := s.limits()
	rows, err := s.DB.Query(`SELECT id,role,content,token_count FROM conversation_messages WHERE user_id=? AND workshop_id=? AND session_id=? ORDER BY id DESC LIMIT ?`, sc.UserID, sc.WorkshopID, sc.SessionID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	total := 0
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Tokens); err != nil {
			return nil, err
		}
		if total+m.Tokens > budget {
			break
		}
		total += m.Tokens
		result = append(result, m)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, rows.Err()
}
func validateState(q auth.Querier, sc Scope, state TaskState) error {
	if math.IsNaN(state.Quantity) || math.IsInf(state.Quantity, 0) || state.Quantity < 0 || state.Quantity > 1e9 || len(compact(state)) > 8000 {
		return errors.New("invalid working state")
	}
	if state.ProductID > 0 {
		var n int
		if err := q.QueryRow(`SELECT COUNT(*) FROM products WHERE id=? AND workshop_id=?`, state.ProductID, sc.WorkshopID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return ErrScope
		}
	}
	return nil
}
func (s *Service) CreateWorkingMemory(sc Scope, kind string, state TaskState) (*Task, error) {
	if kind == "" {
		return nil, errors.New("task type required")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	task := &Task{ID: hex.EncodeToString(b), Type: kind, State: state, Status: "active"}
	err := s.tx(sc, func(tx *sql.Tx) error {
		if err := validateState(tx, sc, state); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO working_memory(user_id,workshop_id,task_id,task_type,state_json,status) VALUES(?,?,?,?,?,'active')`, sc.UserID, sc.WorkshopID, task.ID, kind, compact(state))
		return err
	})
	return task, err
}
func (s *Service) ActiveWorking(sc Scope) (*Task, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	t := &Task{}
	var raw string
	err := s.DB.QueryRow(`SELECT task_id,task_type,state_json,status FROM working_memory WHERE user_id=? AND workshop_id=? AND status IN ('active','waiting_input') AND (?='' OR task_id=?)`, sc.UserID, sc.WorkshopID, sc.TaskID, sc.TaskID).Scan(&t.ID, &t.Type, &raw, &t.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &t.State); err != nil {
		return nil, err
	}
	return t, nil
}
func (s *Service) UpdateWorkingMemory(sc Scope, state TaskState, status string) error {
	if status != "active" && status != "waiting_input" {
		return errors.New("invalid active task status")
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := validateState(tx, sc, state); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE working_memory SET state_json=?,status=?,updated_at=CURRENT_TIMESTAMP WHERE task_id=? AND user_id=? AND workshop_id=? AND status IN ('active','waiting_input')`, compact(state), status, sc.TaskID, sc.UserID, sc.WorkshopID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrNoTask
		}
		return nil
	})
}
func (s *Service) CompleteWorkingMemory(sc Scope, cancel bool) error {
	status := "completed"
	if cancel {
		status = "cancelled"
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE working_memory SET status=?,completed_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE task_id=? AND user_id=? AND workshop_id=? AND status IN ('active','waiting_input')`, status, sc.TaskID, sc.UserID, sc.WorkshopID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrNoTask
		}
		return nil
	})
}

var preferenceValues = map[string]map[string]bool{"response_style": {"concise": true, "detailed": true}, "summary_first": {"true": true, "false": true}, "response_format": {"table": true, "text": true}, "language": {"ru": true, "en": true}}

func (s *Service) SaveLongTermMemory(sc Scope, m LongTerm) error {
	if len(m.Value) > 4000 || len(m.Key) > 100 || m.Key == "" {
		return errors.New("invalid persistent memory")
	}
	if IsDomainFact(m.Value) {
		return ErrDomain
	}
	if m.Source == "" {
		m.Source = "explicit_user_confirmation"
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		if m.Type == "USER_PREFERENCE" {
			if m.ScopeType != "user" || !preferenceValues[m.Key][m.Value] {
				return errors.New("unsupported personal preference")
			}
			return personalization.UpdateTx(tx, sc.UserID, sc.WorkshopID, m.Key, m.Value, m.Source)
		}
		var user, workshop, entity any
		switch m.ScopeType {
		case "user":
			if m.Type != "USER_PROFILE" {
				return ErrScope
			}
			user = sc.UserID
		case "workshop", "process", "product":
			if err := auth.Require(tx, sc.UserID, sc.WorkshopID, auth.WorkshopManage); err != nil {
				return err
			}
			workshop = sc.WorkshopID
			valid := (m.ScopeType == "workshop" && (m.Type == "WORKSHOP_DECISION" || m.Type == "WORKSHOP_KNOWLEDGE")) || (m.ScopeType == "process" && m.Type == "PROCESS_RULE") || (m.ScopeType == "product" && m.Type == "PRODUCT_KNOWLEDGE")
			if !valid {
				return ErrScope
			}
			if m.ScopeType == "product" {
				if m.EntityID <= 0 {
					return ErrScope
				}
				if err := validateState(tx, sc, TaskState{ProductID: m.EntityID}); err != nil {
					return err
				}
				entity = m.EntityID
			}
		default:
			return ErrScope
		}
		var old sql.NullString
		_ = tx.QueryRow(`SELECT value_json FROM long_term_memory WHERE scope_type=? AND COALESCE(user_id,0)=COALESCE(?,0) AND COALESCE(workshop_id,0)=COALESCE(?,0) AND COALESCE(entity_id,0)=COALESCE(?,0) AND key=?`, m.ScopeType, user, workshop, entity, m.Key).Scan(&old)
		_, err := tx.Exec(`INSERT INTO long_term_memory(memory_type,category,scope_type,user_id,workshop_id,entity_type,entity_id,key,value_json,source_type,created_by_user_id) VALUES(?,?,?,?,?,'product',?,?,?,?,?) ON CONFLICT DO UPDATE SET value_json=excluded.value_json,category=excluded.category,memory_type=excluded.memory_type,source_type=excluded.source_type,created_by_user_id=excluded.created_by_user_id,updated_at=CURRENT_TIMESTAMP,is_active=1,version=long_term_memory.version+1`, m.Type, m.Category, m.ScopeType, user, workshop, entity, m.Key, compact(m.Value), m.Source, sc.UserID)
		if err != nil {
			return err
		}
		return memoryAudit(tx, sc, "MEMORY_LONG_TERM_SAVED", m.Key, old.String, m.Value, m.Source)
	})
}
func (s *Service) UpdateLongTermMemory(sc Scope, m LongTerm) error {
	return s.SaveLongTermMemory(sc, m)
}
func (s *Service) DeactivateLongTermMemory(sc Scope, id int64) error {
	return s.tx(sc, func(tx *sql.Tx) error {
		var owner, workshop sql.NullInt64
		var typ, key, old string
		if err := tx.QueryRow(`SELECT user_id,workshop_id,scope_type,key,value_json FROM long_term_memory WHERE id=? AND is_active=1`, id).Scan(&owner, &workshop, &typ, &key, &old); err != nil {
			return err
		}
		if typ == "user" {
			if owner.Int64 != sc.UserID {
				return ErrScope
			}
		} else {
			if workshop.Int64 != sc.WorkshopID {
				return ErrScope
			}
			if err := auth.Require(tx, sc.UserID, sc.WorkshopID, auth.WorkshopManage); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE long_term_memory SET is_active=0,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE id=?`, id); err != nil {
			return err
		}
		return memoryAudit(tx, sc, "MEMORY_DEACTIVATED", key, old, nil, "explicit_user_confirmation")
	})
}
func (s *Service) RelevantLongTerm(sc Scope, query, taskType string, productID int64) ([]LongTerm, error) {
	if err := s.authorize(s.DB, sc); err != nil {
		return nil, err
	}
	out := []LongTerm{}
	var raw string
	var version int
	err := s.DB.QueryRow(`SELECT settings_json,version FROM user_preferences WHERE user_id=?`, sc.UserID).Scan(&raw, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if raw != "" {
		settings := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return nil, err
		}
		for _, key := range []string{"response_style", "summary_first", "response_format", "language"} {
			value, ok := settings[key].(string)
			if ok && preferenceValues[key][value] {
				out = append(out, LongTerm{Type: "USER_PREFERENCE", ScopeType: "user", Category: "response", Key: key, Value: value, Source: "user_preferences", Version: version})
			}
		}
	}
	query = strings.ToLower(query)
	if strings.Contains(query, "профил") {
		query += " profile"
	}
	// SQL narrows scope AND task/category/entity/key before applying the hard retrieval limit.
	rows, err := s.DB.Query(`SELECT id,memory_type,category,scope_type,COALESCE(entity_id,0),key,value_json,source_type,version FROM long_term_memory WHERE is_active=1 AND ((scope_type='user' AND user_id=?) OR (scope_type IN ('workshop','process') AND workshop_id=?) OR (scope_type='product' AND workshop_id=? AND entity_id=?)) AND (category=? OR (scope_type='product' AND entity_id=?) OR instr(?,key)>0 OR (category='quality_control' AND (? LIKE '%контрол%' OR ? LIKE '%провер%' OR ? IN ('production_plan','assembly')))) ORDER BY updated_at DESC,id DESC LIMIT 8`, sc.UserID, sc.WorkshopID, sc.WorkshopID, productID, taskType, productID, query, query, query, taskType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m LongTerm
		var value string
		if err := rows.Scan(&m.ID, &m.Type, &m.Category, &m.ScopeType, &m.EntityID, &m.Key, &value, &m.Source, &m.Version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(value), &m.Value); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Service) Propose(sc Scope, chat int64, c Candidate) error {
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := session(tx, sc); err != nil {
			return err
		}
		c.Scope = sc
		if _, err := tx.Exec(`DELETE FROM pending_actions WHERE user_id=? AND workshop_id=? AND action_type='memory_candidate'`, sc.UserID, sc.WorkshopID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO pending_actions(workshop_id,chat_id,user_id,action_type,payload,expires_at) VALUES(?,?,?,'memory_candidate',?,?)`, sc.WorkshopID, chat, sc.UserID, compact(c), time.Now().Add(15*time.Minute).UTC().Format(time.RFC3339))
		return err
	})
}
func (s *Service) TakeProposal(sc Scope, chat int64) (Candidate, error) {
	var c Candidate
	err := s.tx(sc, func(tx *sql.Tx) error {
		if err := session(tx, sc); err != nil {
			return err
		}
		var id int64
		var raw string
		if err := tx.QueryRow(`SELECT id,payload FROM pending_actions WHERE user_id=? AND workshop_id=? AND chat_id=? AND action_type='memory_candidate' AND expires_at>? ORDER BY id DESC LIMIT 1`, sc.UserID, sc.WorkshopID, chat, time.Now().UTC().Format(time.RFC3339)).Scan(&id, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
		if c.Scope.UserID != sc.UserID || c.Scope.WorkshopID != sc.WorkshopID || c.Scope.SessionID != sc.SessionID {
			return ErrScope
		}
		_, err := tx.Exec(`DELETE FROM pending_actions WHERE id=?`, id)
		return err
	})
	return c, err
}
func (s *Service) SaveTrace(sc Scope, trace Trace) error {
	return s.tx(sc, func(tx *sql.Tx) error {
		if err := session(tx, sc); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO memory_traces(user_id,workshop_id,session_id,trace_json) VALUES(?,?,?,?)`, sc.UserID, sc.WorkshopID, sc.SessionID, compact(trace)); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM memory_traces WHERE user_id=? AND workshop_id=? AND id NOT IN (SELECT id FROM memory_traces WHERE user_id=? AND workshop_id=? ORDER BY id DESC LIMIT 20)`, sc.UserID, sc.WorkshopID, sc.UserID, sc.WorkshopID)
		return err
	})
}
func (s *Service) LastTrace(sc Scope) (Trace, error) {
	var trace Trace
	if err := s.authorize(s.DB, sc); err != nil {
		return trace, err
	}
	var raw string
	err := s.DB.QueryRow(`SELECT trace_json FROM memory_traces WHERE user_id=? AND workshop_id=? AND session_id=? ORDER BY id DESC LIMIT 1`, sc.UserID, sc.WorkshopID, sc.SessionID).Scan(&raw)
	if err != nil {
		return trace, err
	}
	err = json.Unmarshal([]byte(raw), &trace)
	return trace, err
}
func (s *Service) ForgetPreference(sc Scope, key string) error {
	if _, ok := preferenceValues[key]; !ok {
		return fmt.Errorf("unknown preference")
	}
	return s.tx(sc, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRow(`SELECT settings_json FROM user_preferences WHERE user_id=?`, sc.UserID).Scan(&raw); err != nil {
			return err
		}
		settings := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return err
		}
		old := settings[key]
		delete(settings, key)
		if _, err := tx.Exec(`UPDATE user_preferences SET settings_json=?,version=version+1,updated_at=CURRENT_TIMESTAMP WHERE user_id=?`, compact(settings), sc.UserID); err != nil {
			return err
		}
		return memoryAudit(tx, sc, "MEMORY_PREFERENCE_REMOVED", key, old, nil, "explicit_user_confirmation")
	})
}
