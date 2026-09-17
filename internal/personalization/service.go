// Package personalization is the typed view of existing user_preferences.
package personalization

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"workshop-agent/internal/auth"
)

type Profile struct {
	UserID       int64    `json:"user_id"`
	Style        string   `json:"style"`
	Detail       string   `json:"detail_level"`
	Language     string   `json:"language"`
	Format       string   `json:"format"`
	SummaryFirst bool     `json:"summary_first"`
	Confirmation string   `json:"confirmation_level"`
	HideLLM      bool     `json:"hide_llm_details"`
	Constraints  []string `json:"constraints"`
	UpdatedAt    string   `json:"updated_at"`
}
type Resolution struct {
	Stored          Profile           `json:"profile"`
	Overrides       map[string]string `json:"override"`
	Applied         Profile           `json:"applied"`
	Context         string            `json:"context"`
	EstimatedTokens int               `json:"profile_context_tokens_estimate"`
}
type Service struct {
	DB     *sql.DB
	UserID int64
}

func New(db *sql.DB) *Service                { return &Service{DB: db} }
func (s *Service) ForUser(id int64) *Service { c := *s; c.UserID = id; return &c }
func active(q auth.Querier, id int64) error {
	var n int
	if err := q.QueryRow("SELECT COUNT(*) FROM users WHERE id=? AND status='active' AND is_active=1", id).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return auth.ErrDisabled
	}
	return nil
}
func Ensure(db *sql.DB, id int64) error {
	if err := active(db, id); err != nil {
		return err
	}
	_, err := db.Exec("INSERT OR IGNORE INTO user_preferences(user_id,settings_json) VALUES(?,'{}')", id)
	return err
}
func Default(id int64) Profile {
	return Profile{UserID: id, Style: "neutral", Detail: "normal", Language: "ru", Format: "text", Confirmation: "confirm_destructive", Constraints: []string{"do_not_modify_bom_without_confirmation", "do_not_create_production_without_confirmation"}}
}

var Allowed = map[string][]string{"style": {"neutral", "concise", "friendly", "technical", "formal"}, "response_style": {"normal", "concise", "detailed"}, "response_format": {"text", "list", "table"}, "summary_first": {"false", "true"}, "language": {"ru", "en"}, "confirmation_level": {"always_confirm", "confirm_destructive", "minimal_confirmation"}, "hide_llm_details": {"false", "true"}}

func valid(key, value string) bool {
	for _, v := range Allowed[key] {
		if v == value {
			return true
		}
	}
	return false
}
func apply(p *Profile, key, value string) {
	if !valid(key, value) {
		return
	}
	switch key {
	case "style":
		p.Style = value
	case "response_style":
		p.Detail = value
		if value == "concise" {
			p.Detail = "brief"
		}
	case "response_format":
		p.Format = value
	case "summary_first":
		p.SummaryFirst = value == "true"
	case "language":
		p.Language = value
	case "confirmation_level":
		p.Confirmation = value
	case "hide_llm_details":
		p.HideLLM = value == "true"
	}
}
func (s *Service) GetProfile() (Profile, error) {
	p := Default(s.UserID)
	if err := Ensure(s.DB, s.UserID); err != nil {
		return p, err
	}
	var raw string
	if err := s.DB.QueryRow("SELECT settings_json,updated_at FROM user_preferences WHERE user_id=?", s.UserID).Scan(&raw, &p.UpdatedAt); err != nil {
		return p, err
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return p, err
	}
	for k, v := range settings {
		if val, ok := v.(string); ok {
			apply(&p, k, val)
		}
	}
	return p, nil
}
func UpdateTx(tx *sql.Tx, user, workshop int64, key, value, source string) error {
	if err := active(tx, user); err != nil {
		return err
	}
	if !valid(key, value) {
		return errors.New("Неподдерживаемая настройка профиля")
	}
	var raw string
	err := tx.QueryRow("SELECT settings_json FROM user_preferences WHERE user_id=?", user).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	settings := map[string]any{}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &settings); err != nil {
			return err
		}
	}
	old := settings[key]
	if old == value {
		return nil
	}
	settings[key] = value
	blob, _ := json.Marshal(settings)
	if _, err = tx.Exec("INSERT INTO user_preferences(user_id,settings_json,version,updated_at) VALUES(?,?,1,CURRENT_TIMESTAMP) ON CONFLICT(user_id) DO UPDATE SET settings_json=excluded.settings_json,version=user_preferences.version+1,updated_at=CURRENT_TIMESTAMP", user, string(blob)); err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]any{"scope": "user", "key": key, "source": source, "before": old, "after": value})
	_, err = tx.Exec("INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,action,field_name,old_value,new_value,details,event_type,metadata_json,target_user_id) VALUES(?,'user_profile',?,?,'update',?,?,?,'Personalization preference','PROFILE_UPDATED',?,?)", workshop, user, user, key, fmt.Sprint(old), value, string(meta), user)
	return err
}
func (s *Service) UpdatePreference(key, value, source string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE schema_migrations SET name=name WHERE number=101"); err != nil {
		return err
	}
	if err = UpdateTx(tx, s.UserID, 0, key, value, source); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Reset() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE schema_migrations SET name=name WHERE number=101"); err != nil {
		return err
	}
	defaults := map[string]string{"style": "neutral", "response_style": "normal", "response_format": "text", "summary_first": "false", "language": "ru", "confirmation_level": "confirm_destructive", "hide_llm_details": "false"}
	for k, v := range defaults {
		if err = UpdateTx(tx, s.UserID, 0, k, v, "profile_reset"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func Normalize(text string) string {
	return strings.Trim(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(text)), "ё", "е"), ".! ")
}
func PersistentIntent(text string) (string, string, bool) {
	switch Normalize(text) {
	case "запомни, что мне нравятся короткие ответы", "запомни, мне нравятся короткие ответы", "всегда отвечай коротко", "всегда отвечай кратко", "я люблю короткие ответы":
		return "response_style", "concise", true
	case "теперь всегда отвечай подробно", "всегда отвечай мне подробно", "всегда отвечай подробно", "запомни, я хочу подробные ответы":
		return "response_style", "detailed", true
	case "всегда сначала показывай итог", "запомни, сначала показывай итог":
		return "summary_first", "true", true
	case "таблицы мне удобнее списков", "всегда отвечай таблицей":
		return "response_format", "table", true
	case "всегда отвечай списком":
		return "response_format", "list", true
	case "в отчетах не показывай технические детали llm":
		return "hide_llm_details", "true", true
	}
	return "", "", false
}
func Overrides(text string) map[string]string {
	out := map[string]string{}
	if _, _, persistent := PersistentIntent(text); persistent {
		return out
	}
	t := Normalize(text)
	if strings.Contains(t, "в этой мастерской") || strings.Contains(t, "запомни") || strings.Contains(t, "всегда") {
		return out
	}
	if strings.Contains(t, "распиши подробно") || strings.Contains(t, "объясни подробно") || strings.Contains(t, "ответь подробно") {
		out["response_style"] = "detailed"
	}
	if strings.Contains(t, "ответь кратко") || strings.Contains(t, "ответь коротко") {
		out["response_style"] = "concise"
	}
	if strings.Contains(t, "в этот раз таблицей") {
		out["response_format"] = "table"
	}
	if strings.Contains(t, "в этот раз списком") {
		out["response_format"] = "list"
	}
	return out
}
func (s *Service) ResolveProfile(text string, taskPreferences map[string]string) (Resolution, error) {
	p, err := s.GetProfile()
	if err != nil {
		return Resolution{}, err
	}
	r := Resolution{Stored: p, Applied: p, Overrides: Overrides(text)}
	for k, v := range taskPreferences {
		apply(&r.Applied, k, v)
	}
	for k, v := range r.Overrides {
		apply(&r.Applied, k, v)
	}
	// English is stored for future UI support; this version consistently serves Russian.
	r.Applied.Language = "ru"
	r.Context = BuildContext(r.Applied)
	r.EstimatedTokens = (len([]byte(r.Context)) + 2) / 3
	return r, nil
}
func BuildContext(p Profile) string {
	return fmt.Sprintf("[USER PROFILE]\nStyle: %s\nDetail level: %s\nFormat: %s; summary_first=%t; show_units=true\nLanguage: %s\nInteraction preference: %s\nConstraints: confirm BOM and production mutations; hide_llm_details=%t\nPriority: security/authorization/business rules > current explicit request > working task requirements > profile > personal memory > defaults. Preferences affect presentation only, never permissions, facts, required confirmations or command schema. Show a concise calculation summary if requested, not private reasoning.\n", p.Style, p.Detail, p.Format, p.SummaryFirst, p.Language, p.Confirmation, p.HideLLM)
}
func (s *Service) SaveTrace(workshop int64, r Resolution) error {
	if err := active(s.DB, s.UserID); err != nil {
		return err
	}
	raw, _ := json.Marshal(r)
	_, err := s.DB.Exec("INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,action,details,event_type,metadata_json,target_user_id) VALUES(?,'user_profile',?,?,'resolve','Personalization trace','PROFILE_RESOLVED',?,?)", workshop, s.UserID, s.UserID, string(raw), s.UserID)
	return err
}
func (s *Service) LastTrace() (Resolution, error) {
	var r Resolution
	if err := active(s.DB, s.UserID); err != nil {
		return r, err
	}
	var raw string
	err := s.DB.QueryRow("SELECT metadata_json FROM audit_logs WHERE actor_user_id=? AND entity_type='user_profile' AND event_type='PROFILE_RESOLVED' ORDER BY id DESC LIMIT 1", s.UserID).Scan(&raw)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(raw), &r)
	return r, err
}
