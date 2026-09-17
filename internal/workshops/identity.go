package workshops

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"workshop-agent/internal/auth"
)

var ErrInvite = errors.New("Приглашение недействительно, истекло, отозвано или уже использовано.")
var ErrMemberExists = errors.New("Вы уже состоите в этой мастерской. Для восстановления отключённого доступа обратитесь к администратору.")
var ErrOwner = errors.New("Владельца нельзя удалить, отключить или сменить ему роль. Сначала явно передайте владение.")

type Member struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	WorkshopID int64     `json:"workshop_id"`
	Name       string    `json:"name"`
	Username   string    `json:"username"`
	Role       auth.Role `json:"role"`
	Status     string    `json:"status"`
	JoinedAt   string    `json:"joined_at"`
}
type WorkshopSummary struct {
	ID   int64
	Name string
	Role auth.Role
}
type Invite struct {
	ID              int64
	WorkshopID      int64
	WorkshopName    string
	CreatedByUserID int64
	Role            auth.Role
	Status          string
	ExpiresAt       int64
	MaxUses         int
	UsedCount       int
	CreatedAt       string
}

// UpsertUser never uses a username to identify a person.
func (s *Service) UpsertUser(telegramID int64, username, first, last string) (int64, error) {
	if telegramID <= 0 {
		return 0, auth.ErrDenied
	}
	_, err := s.DB().Exec(`INSERT INTO users(telegram_user_id,telegram_username,first_name,last_name,display_name,updated_at,status) VALUES(?,?,?,?,?,CURRENT_TIMESTAMP,'active') ON CONFLICT(telegram_user_id) DO UPDATE SET telegram_username=excluded.telegram_username,first_name=excluded.first_name,last_name=excluded.last_name,display_name=excluded.display_name,updated_at=CURRENT_TIMESTAMP`, telegramID, nullable(username), nullable(first), nullable(last), strings.TrimSpace(first+" "+last))
	if err != nil {
		return 0, err
	}
	var id int64
	var status string
	var active int
	if err := s.DB().QueryRow(`SELECT id,status,is_active FROM users WHERE telegram_user_id=?`, telegramID).Scan(&id, &status, &active); err != nil {
		return 0, err
	}
	if status != "active" || active != 1 {
		return 0, auth.ErrDisabled
	}
	return id, nil
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func userActive(tx *sql.Tx, id int64) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE id=? AND status='active' AND is_active=1`, id).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return auth.ErrDenied
	}
	return nil
}

// A write lock is acquired before reads to serialize invite accepts and ownership changes.
func (s *Service) transaction(fn func(*sql.Tx) error) error {
	tx, err := s.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE schema_migrations SET name=name WHERE number=100`); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func event(tx *sql.Tx, workshop, actor, target int64, kind string, metadata any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var targetID any
	if target > 0 {
		targetID = target
	}
	_, err = tx.Exec(`INSERT INTO audit_logs(workshop_id,entity_type,entity_id,actor_user_id,actor_name,action,field_name,old_value,new_value,details,target_user_id,event_type,metadata_json) VALUES(?,'identity_access',? ,?,'',?,'','','',?,?,?,?)`, workshop, workshop, actor, kind, string(raw), targetID, kind, string(raw))
	return err
}
func setActive(tx *sql.Tx, user, workshop int64) error {
	if err := auth.Require(tx, user, workshop, auth.WorkshopRead); err != nil {
		return err
	}
	var old sql.NullInt64
	err := tx.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, user).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if old.Valid && old.Int64 == workshop {
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO user_workshop_context(user_id,active_workshop_id) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET active_workshop_id=excluded.active_workshop_id,updated_at=CURRENT_TIMESTAMP`, user, workshop); err != nil {
		return err
	}
	return event(tx, workshop, user, user, "ACTIVE_WORKSHOP_CHANGED", map[string]any{"previous_workshop_id": old.Int64})
}
func (s *Service) CreateOwnedWorkshop(user int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 120 {
		return 0, errors.New("Введите название мастерской длиной от 1 до 120 символов.")
	}
	var id int64
	err := s.transaction(func(tx *sql.Tx) error {
		if err := userActive(tx, user); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO workshops(name,updated_at,status) VALUES(?,CURRENT_TIMESTAMP,'active')`, name)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO workshop_members(workshop_id,user_id,role,joined_at) VALUES(?,?,'OWNER',CURRENT_TIMESTAMP)`, id, user); err != nil {
			return err
		}
		if err := event(tx, id, user, user, "WORKSHOP_CREATED", map[string]any{"name": name}); err != nil {
			return err
		}
		return setActive(tx, user, id)
	})
	return id, err
}
func (s *Service) AvailableWorkshops(user int64) ([]WorkshopSummary, error) {
	rows, err := s.DB().Query(`SELECT w.id,w.name,m.role FROM workshops w JOIN workshop_members m ON m.workshop_id=w.id JOIN users u ON u.id=m.user_id WHERE m.user_id=? AND m.status='active' AND m.is_active=1 AND w.status='active' AND w.is_active=1 AND u.status='active' AND u.is_active=1 ORDER BY w.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkshopSummary{}
	for rows.Next() {
		var w WorkshopSummary
		if err := rows.Scan(&w.ID, &w.Name, &w.Role); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Service) SetActiveWorkshop(user, workshop int64) error {
	return s.transaction(func(tx *sql.Tx) error { return setActive(tx, user, workshop) })
}
func (s *Service) ActiveWorkshop(user int64) (int64, error) {
	var id int64
	err := s.transaction(func(tx *sql.Tx) error {
		if err := userActive(tx, user); err != nil {
			return err
		}
		var active sql.NullInt64
		err := tx.QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, user).Scan(&active)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if active.Valid {
			if err := auth.Require(tx, user, active.Int64, auth.WorkshopRead); err == nil {
				id = active.Int64
				return nil
			} else if !errors.Is(err, auth.ErrDenied) && !errors.Is(err, auth.ErrDisabled) {
				return err
			}
			if _, err := tx.Exec(`UPDATE user_workshop_context SET active_workshop_id=NULL,updated_at=CURRENT_TIMESTAMP WHERE user_id=?`, user); err != nil {
				return err
			}
			// Commit the reset and ask for an explicit selection, even if one remains.
			return event(tx, active.Int64, user, user, "ACTIVE_WORKSHOP_CHANGED", map[string]any{"reason": "access_lost", "active_workshop_id": nil})
		}
		var count int
		var only sql.NullInt64
		if err := tx.QueryRow(`SELECT COUNT(*),MIN(w.id) FROM workshops w JOIN workshop_members m ON m.workshop_id=w.id WHERE m.user_id=? AND m.status='active' AND m.is_active=1 AND w.status='active' AND w.is_active=1`, user).Scan(&count, &only); err != nil {
			return err
		}
		if count == 1 {
			id = only.Int64
			return setActive(tx, user, id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, auth.ErrChooseWorkshop
	}
	return id, nil
}
func (s *Service) Members(actor, workshop int64) ([]Member, error) {
	if err := auth.Require(s.DB(), actor, workshop, auth.MembersRead); err != nil {
		return nil, err
	}
	rows, err := s.DB().Query(`SELECT m.id,m.user_id,m.workshop_id,COALESCE(u.display_name,''),COALESCE(u.telegram_username,''),m.role,m.status,COALESCE(m.joined_at,'') FROM workshop_members m JOIN users u ON u.id=m.user_id WHERE m.workshop_id=? ORDER BY m.id`, workshop)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.UserID, &m.WorkshopID, &m.Name, &m.Username, &m.Role, &m.Status, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Service) CreateInvite(actor, workshop int64, role auth.Role, ttl time.Duration, maxUses int) (Invite, string, error) {
	if !auth.Assignable(role) {
		return Invite{}, "", auth.ErrDenied
	}
	if ttl == 0 {
		ttl = s.InviteTTL
	}
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	if maxUses == 0 {
		maxUses = s.InviteMaxUses
	}
	if maxUses == 0 {
		maxUses = 1
	}
	if ttl < 0 || maxUses < 1 {
		return Invite{}, "", errors.New("Некорректный срок или число использований приглашения.")
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return Invite{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	var inv Invite
	err := s.transaction(func(tx *sql.Tx) error {
		if err := auth.Require(tx, actor, workshop, auth.MembersInvite); err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO workshop_invites(workshop_id,created_by_user_id,role,token_hash,expires_at,max_uses) VALUES(?,?,?,?,?,?)`, workshop, actor, role, hashToken(token), time.Now().Add(ttl).Unix(), maxUses)
		if err != nil {
			return err
		}
		inv.ID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return event(tx, workshop, actor, 0, "MEMBER_INVITED", map[string]any{"invite_id": inv.ID, "role": role})
	})
	if err != nil {
		return Invite{}, "", err
	}
	inv, err = s.PreviewInvite(token)
	return inv, token, err
}
func inviteFrom(q auth.Querier, token string) (Invite, error) {
	var i Invite
	if len(token) != 43 {
		return i, ErrInvite
	}
	err := q.QueryRow(`SELECT i.id,i.workshop_id,w.name,i.created_by_user_id,i.role,i.status,i.expires_at,i.max_uses,i.used_count,i.created_at FROM workshop_invites i JOIN workshops w ON w.id=i.workshop_id WHERE i.token_hash=? AND w.status='active' AND w.is_active=1`, hashToken(token)).Scan(&i.ID, &i.WorkshopID, &i.WorkshopName, &i.CreatedByUserID, &i.Role, &i.Status, &i.ExpiresAt, &i.MaxUses, &i.UsedCount, &i.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrInvite
	}
	if err != nil {
		return i, err
	}
	if i.Status != "active" || i.ExpiresAt <= time.Now().Unix() || i.UsedCount >= i.MaxUses {
		return i, ErrInvite
	}
	return i, nil
}
func (s *Service) PreviewInvite(token string) (Invite, error) { return inviteFrom(s.DB(), token) }
func (s *Service) AcceptInvite(user int64, token string) (int64, error) {
	var workshop int64
	err := s.transaction(func(tx *sql.Tx) error {
		if err := userActive(tx, user); err != nil {
			return err
		}
		i, err := inviteFrom(tx, token)
		if err != nil {
			return err
		}
		workshop = i.WorkshopID
		var status string
		err = tx.QueryRow(`SELECT status FROM workshop_members WHERE user_id=? AND workshop_id=?`, user, workshop).Scan(&status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			return ErrMemberExists
		}
		res, err := tx.Exec(`UPDATE workshop_invites SET used_count=used_count+1,status=CASE WHEN used_count+1>=max_uses THEN 'used' ELSE 'active' END WHERE id=? AND status='active' AND expires_at>? AND used_count<max_uses`, i.ID, time.Now().Unix())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrInvite
		}
		if _, err := tx.Exec(`INSERT INTO workshop_members(workshop_id,user_id,role,invited_by_user_id,joined_at) VALUES(?,?,?,?,CURRENT_TIMESTAMP)`, workshop, user, i.Role, i.CreatedByUserID); err != nil {
			return err
		}
		if err := event(tx, workshop, user, user, "INVITE_ACCEPTED", map[string]any{"invite_id": i.ID, "role": i.Role}); err != nil {
			return err
		}
		return setActive(tx, user, workshop)
	})
	return workshop, err
}
func (s *Service) Invites(actor, workshop int64) ([]Invite, error) {
	if err := auth.Require(s.DB(), actor, workshop, auth.MembersInvite); err != nil {
		return nil, err
	}
	rows, err := s.DB().Query(`SELECT id,workshop_id,created_by_user_id,role,CASE WHEN status='active' AND expires_at<=? THEN 'expired' ELSE status END,expires_at,max_uses,used_count,created_at FROM workshop_invites WHERE workshop_id=? ORDER BY id DESC`, time.Now().Unix(), workshop)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.WorkshopID, &i.CreatedByUserID, &i.Role, &i.Status, &i.ExpiresAt, &i.MaxUses, &i.UsedCount, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Service) RevokeInvite(actor, workshop, id int64) error {
	return s.transaction(func(tx *sql.Tx) error {
		if err := auth.Require(tx, actor, workshop, auth.MembersInvite); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE workshop_invites SET status='revoked',revoked_at=CURRENT_TIMESTAMP WHERE id=? AND workshop_id=? AND status='active'`, id, workshop)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrInvite
		}
		return event(tx, workshop, actor, 0, "INVITE_REVOKED", map[string]any{"invite_id": id})
	})
}
func (s *Service) ChangeRole(actor, workshop, target int64, role auth.Role) error {
	if !auth.Assignable(role) {
		return auth.ErrDenied
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := auth.Require(tx, actor, workshop, auth.MembersManage); err != nil {
			return err
		}
		var old auth.Role
		if err := tx.QueryRow(`SELECT role FROM workshop_members WHERE workshop_id=? AND user_id=? AND status IN ('active','disabled')`, workshop, target).Scan(&old); err != nil {
			return auth.ErrDenied
		}
		if old == auth.Owner {
			return ErrOwner
		}
		if _, err := tx.Exec(`UPDATE workshop_members SET role=?,updated_at=CURRENT_TIMESTAMP WHERE workshop_id=? AND user_id=?`, role, workshop, target); err != nil {
			return err
		}
		return event(tx, workshop, actor, target, "MEMBER_ROLE_CHANGED", map[string]any{"old_role": old, "role": role})
	})
}
func (s *Service) ChangeMemberStatus(actor, workshop, target int64, status string) error {
	events := map[string]string{"active": "MEMBER_ENABLED", "disabled": "MEMBER_DISABLED", "removed": "MEMBER_REMOVED", "left": "MEMBER_LEFT"}
	kind, ok := events[status]
	if !ok {
		return errors.New("Недопустимый статус участника.")
	}
	return s.transaction(func(tx *sql.Tx) error {
		p := auth.MembersManage
		if status == "left" {
			if actor != target {
				return auth.ErrDenied
			}
			p = auth.WorkshopRead
		}
		if err := auth.Require(tx, actor, workshop, p); err != nil {
			return err
		}
		var role auth.Role
		var old string
		if err := tx.QueryRow(`SELECT role,status FROM workshop_members WHERE workshop_id=? AND user_id=?`, workshop, target).Scan(&role, &old); err != nil {
			return auth.ErrDenied
		}
		if role == auth.Owner {
			return ErrOwner
		}
		if old == status {
			return nil
		}
		if status == "active" {
			if err := userActive(tx, target); err != nil {
				return err
			}
		}
		active := 0
		if status == "active" {
			active = 1
		}
		if _, err := tx.Exec(`UPDATE workshop_members SET status=?,is_active=?,updated_at=CURRENT_TIMESTAMP WHERE workshop_id=? AND user_id=?`, status, active, workshop, target); err != nil {
			return err
		}
		// Preserve a stale context until resolution, which clears it and requests selection.
		return event(tx, workshop, actor, target, kind, map[string]any{"old_status": old, "status": status})
	})
}
func (s *Service) TransferOwnership(workshop, from, to int64) error {
	if from == to {
		return errors.New("Выберите другого активного участника.")
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := auth.Require(tx, from, workshop, auth.OwnershipTransfer); err != nil {
			return err
		}
		if err := auth.Require(tx, to, workshop, auth.WorkshopRead); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE workshop_members SET role='OWNER',updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND workshop_id=?`, to, workshop); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE workshop_members SET role='ADMIN',updated_at=CURRENT_TIMESTAMP WHERE user_id=? AND workshop_id=?`, from, workshop); err != nil {
			return err
		}
		return event(tx, workshop, from, to, "OWNERSHIP_TRANSFERRED", map[string]any{"previous_owner_id": from, "new_owner_id": to})
	})
}
func (s *Service) PermissionTrace(user, workshop int64) (map[string]any, error) {
	var role auth.Role
	var status string
	var active sql.NullInt64
	err := s.DB().QueryRow(`SELECT role,status FROM workshop_members WHERE user_id=? AND workshop_id=?`, user, workshop).Scan(&role, &status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	_ = s.DB().QueryRow(`SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?`, user).Scan(&active)
	access := auth.Require(s.DB(), user, workshop, auth.WorkshopRead)
	permissions := []auth.Permission{}
	if access == nil {
		permissions = auth.Permissions(role)
	}
	return map[string]any{"user_id": user, "workshop_id": workshop, "membership_status": status, "role": role, "permissions": permissions, "active_workshop_id": active.Int64, "access": access == nil}, nil
}
func (s *Service) UserSettings(user int64) (string, error) {
	var settings string
	err := s.DB().QueryRow(`SELECT COALESCE(p.settings_json,'{}') FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE u.id=? AND u.status='active'`, user).Scan(&settings)
	return settings, err
}
func (s *Service) SetUserSettings(user int64, settings string) error {
	if !json.Valid([]byte(settings)) {
		return fmt.Errorf("invalid settings JSON")
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := userActive(tx, user); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO user_preferences(user_id,settings_json) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET settings_json=excluded.settings_json`, user, settings)
		return err
	})
}
func (s *Service) SetWorkshopSettings(actor, workshop int64, settings string) error {
	if !json.Valid([]byte(settings)) {
		return fmt.Errorf("invalid settings JSON")
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := auth.Require(tx, actor, workshop, auth.WorkshopManage); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO workshop_settings(workshop_id,settings_json) VALUES(?,?) ON CONFLICT(workshop_id) DO UPDATE SET settings_json=excluded.settings_json`, workshop, settings)
		return err
	})
}
