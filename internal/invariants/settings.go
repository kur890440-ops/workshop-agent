package invariants

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"workshop-agent/internal/auth"
)

const MaterialCheck = "requires_material_check_before_production"

var ErrProtected = errors.New("Это правило защищено кодом и не может быть изменено через Telegram.")
var ErrConfirmation = errors.New("Требуется отдельное подтверждение изменения правила.")
var ErrVersion = errors.New("Правило изменилось. Откройте настройки заново.")

func (r InvariantRegistry) Workshop(q auth.Querier, user, w int64) ([]Rule, error) {
	if e := auth.Require(q, user, w, auth.WorkshopRead); e != nil {
		return nil, e
	}
	rules := r.System()
	v := Rule{ID: fmt.Sprintf("%d:%s", w, MaterialCheck), Key: MaterialCheck, Title: "Проверять достаточность материалов перед запуском производства", Description: "Предварительная проверка; отключение не разрешает отрицательные остатки при выпуске.", Category: "BUSINESS_RULE", ScopeType: "WORKSHOP", ScopeID: &w, Severity: "WARNING", Enforcement: "HARD", IsActive: false, Version: 0, Modifiable: true, CreatedAt: "2026-09-17", UpdatedAt: "2026-09-17", Actions: []string{"start_production"}}
	e := q.QueryRow("SELECT is_active,version,created_at,updated_at FROM invariant_settings WHERE workshop_id=? AND key=?", w, MaterialCheck).Scan(&v.IsActive, &v.Version, &v.CreatedAt, &v.UpdatedAt)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	return append(rules, v), nil
}
func UpdateConfirmed(db *sql.DB, user, w int64, key string, active bool, version int, confirmed bool) error {
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE schema_migrations SET name=name WHERE number=107"); e != nil {
		return e
	}
	if e = auth.Require(tx, user, w, auth.WorkshopManage); e != nil {
		return e
	}
	var current int64
	if e = tx.QueryRow("SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?", user).Scan(&current); e != nil {
		return e
	}
	if current != w {
		return auth.ErrDenied
	}
	if key != MaterialCheck {
		return ErrProtected
	}
	if !confirmed {
		return ErrConfirmation
	}
	rules, e := (InvariantRegistry{}).Workshop(tx, user, w)
	if e != nil {
		return e
	}
	old := rules[len(rules)-1]
	if old.Version != version {
		return ErrVersion
	}
	if _, e = tx.Exec(`INSERT INTO invariant_settings(workshop_id,key,is_active,version) VALUES(?,?,?,?) ON CONFLICT(workshop_id,key) DO UPDATE SET is_active=excluded.is_active,version=excluded.version,updated_at=CURRENT_TIMESTAMP`, w, key, active, version+1); e != nil {
		return e
	}
	before, _ := json.Marshal(old)
	old.IsActive = active
	old.Version++
	after, _ := json.Marshal(old)
	if _, e = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,action,field_name,old_value,new_value,details,event_type) VALUES(?,'invariant',0,?,'update',?,?,?,'Explicit confirmation','INVARIANT_UPDATED')`, w, user, key, string(before), string(after)); e != nil {
		return e
	}
	return tx.Commit()
}
func SaveTrace(db *sql.DB, r Result) error {
	if e := auth.Require(db, r.Action.UserID, r.Action.WorkshopID, auth.WorkshopRead); e != nil {
		return e
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = db.Exec("INSERT INTO invariant_traces(user_id,workshop_id,result_json) VALUES(?,?,?)", r.Action.UserID, r.Action.WorkshopID, string(raw))
	return e
}
func LastTrace(db *sql.DB, user, w int64) (string, error) {
	if e := auth.Require(db, user, w, auth.WorkshopRead); e != nil {
		return "", e
	}
	var raw string
	e := db.QueryRow("SELECT result_json FROM invariant_traces WHERE user_id=? AND workshop_id=? ORDER BY id DESC LIMIT 1", user, w).Scan(&raw)
	return raw, e
}
