package webchat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/message"
	"workshop-agent/internal/stability"
)

var ErrLogin = errors.New("Код неверен, истёк или уже использован. Откройте /chat для нового кода.")
var ErrLimit = errors.New("Слишком много попыток. Подождите минуту и попробуйте снова.")

const MaxAttempts = 5

type Session struct{ ID, UserID, TelegramUserID int64 }
type bucket struct {
	count int
	until time.Time
}
type Service struct {
	admissionMu sync.Mutex
	DB          *sql.DB
	Config      Config
	Now         func() time.Time
	Handle      func(context.Context, message.Inbound) error
	mu          sync.Mutex
	limits      map[string]bucket
	Runtime     *stability.Runtime
	Root        context.Context
	jobsMu      sync.Mutex
	jobs        map[string]*stability.Job
}

func New(db *sql.DB, c Config) (*Service, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	settings, e := stability.NewStore(db, stability.Defaults())
	if e != nil {
		return nil, e
	}
	return &Service{DB: db, Config: c, Now: time.Now, limits: map[string]bucket{}, Runtime: stability.New(settings), Root: context.Background(), jobs: map[string]*stability.Job{}}, nil
}
func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}
func hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (s *Service) allow(key string, max int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	for k, b := range s.limits {
		if !now.Before(b.until) {
			delete(s.limits, k)
		}
	}
	b := s.limits[key]
	if b.until.IsZero() {
		if len(s.limits) >= 10000 {
			return false
		}
		b.until = now.Add(time.Minute)
	}
	if b.count >= max {
		return false
	}
	b.count++
	s.limits[key] = b
	return true
}

// Authorized resolves only existing identities with a currently readable workshop.
// It never provisions a user or a membership.
func (s *Service) Authorized(telegramID int64) (int64, error) {
	return auth.ExistingTelegramUser(s.DB, telegramID)
}
func (s *Service) CreateLogin(ip string) (code, nonce string, err error) {
	if !s.allow("create:"+ip, 5) {
		return "", "", ErrLimit
	}
	for i := 0; i < 5; i++ {
		code, err = random(5)
		if err != nil {
			return
		}
		nonce, err = random(32)
		if err != nil {
			return
		}
		now := s.Now()
		_, err = s.DB.Exec(`INSERT INTO pending_web_logins(id,code_hash,browser_hash,created_at,expires_at) VALUES(?,?,?,?,?)`, code[:4], hash(code), hash(nonce), now.Unix(), now.Add(s.Config.LoginCodeTTL).Unix())
		if err == nil {
			slog.Info("web login created")
			return
		}
	}
	return "", "", errors.New("Не удалось создать код входа")
}
func (s *Service) ConfirmLogin(telegramID int64, code string) (err error) {
	defer func() {
		if err != nil {
			slog.Info("web login failed", "telegram_user_id", telegramID, "reason", loginReason(err))
		}
	}()
	if !s.allow(fmt.Sprintf("telegram:%d", telegramID), 5) {
		return ErrLimit
	}
	user, err := s.Authorized(telegramID)
	if err != nil {
		return err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 8 {
		return ErrLogin
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stored, status string
	var expires int64
	var attempts int
	if err = tx.QueryRow(`SELECT code_hash,status,expires_at,attempt_count FROM pending_web_logins WHERE id=?`, code[:4]).Scan(&stored, &status, &expires, &attempts); err != nil {
		return ErrLogin
	}
	if status != "pending" || expires <= s.Now().Unix() || attempts >= MaxAttempts {
		return ErrLogin
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(hash(code))) != 1 {
		_, err = tx.Exec(`UPDATE pending_web_logins SET attempt_count=attempt_count+1,status=CASE WHEN attempt_count+1>=? THEN 'blocked' ELSE status END WHERE id=?`, MaxAttempts, code[:4])
		if err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return ErrLogin
	}
	now := s.Now()
	result, err := tx.Exec(`INSERT INTO web_sessions(user_id,telegram_user_id,created_at,expires_at,last_seen) VALUES(?,?,?,?,?)`, user, telegramID, now.Unix(), now.Add(s.Config.SessionTTL).Unix(), now.Unix())
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE pending_web_logins SET status='confirmed',session_id=? WHERE id=?`, id, code[:4])
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	slog.Info("web login confirmed; session created", "user_id", user, "telegram_user_id", telegramID, "session_id", id)
	return nil
}
func loginReason(err error) string {
	if errors.Is(err, ErrLimit) {
		return "rate_limit"
	}
	if errors.Is(err, auth.ErrDenied) {
		return "unauthorized"
	}
	return "invalid_or_expired"
}

// Complete is browser-bound and rotates to a fresh credential, atomically and once.
func (s *Service) Complete(nonce string) (string, error) {
	if nonce == "" {
		return "", ErrLogin
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var status string
	var expires int64
	var id sql.NullInt64
	if err = tx.QueryRow(`SELECT status,expires_at,session_id FROM pending_web_logins WHERE browser_hash=?`, hash(nonce)).Scan(&status, &expires, &id); err != nil {
		return "", ErrLogin
	}
	if expires <= s.Now().Unix() || status == "blocked" || status == "consumed" {
		return "", ErrLogin
	}
	if status == "pending" {
		return "", nil
	}
	if status != "confirmed" || !id.Valid {
		return "", ErrLogin
	}
	token, err := random(32)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(`UPDATE web_sessions SET token_hash=? WHERE id=? AND revoked_at IS NULL`, hash(token), id.Int64)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(`UPDATE pending_web_logins SET status='consumed' WHERE browser_hash=?`, hash(nonce))
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}
func (s *Service) Authenticate(token string) (Session, error) {
	var session Session
	err := s.DB.QueryRow(`SELECT id,user_id,telegram_user_id FROM web_sessions WHERE token_hash=? AND expires_at>? AND revoked_at IS NULL`, hash(token), s.Now().Unix()).Scan(&session.ID, &session.UserID, &session.TelegramUserID)
	if err != nil {
		return Session{}, auth.ErrDenied
	}
	user, err := s.Authorized(session.TelegramUserID)
	if err != nil || user != session.UserID {
		return Session{}, auth.ErrDenied
	}
	_, err = s.DB.Exec(`UPDATE web_sessions SET last_seen=? WHERE id=?`, s.Now().Unix(), session.ID)
	return session, err
}
func (s *Service) Revoke(id int64) error {
	_, err := s.DB.Exec(`UPDATE web_sessions SET revoked_at=? WHERE id=?`, s.Now().Unix(), id)
	if err == nil {
		slog.Info("web session revoked", "session_id", id)
	}
	return err
}
func (s *Service) Cleanup(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM pending_web_logins WHERE expires_at<=?`, s.Now().Unix())
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM web_sessions WHERE expires_at<=? OR revoked_at IS NOT NULL`, s.Now().Unix())
	return err
}
func (s *Service) Deliver(chatID int64, out message.Output) error {
	if chatID >= 0 {
		return auth.ErrDenied
	}
	var telegramID int64
	if err := s.DB.QueryRow(`SELECT telegram_user_id FROM web_sessions WHERE id=? AND expires_at>? AND revoked_at IS NULL`, -chatID, s.Now().Unix()).Scan(&telegramID); err != nil {
		return auth.ErrDenied
	}
	if _, err := s.Authorized(telegramID); err != nil {
		return err
	}
	if out.WorkshopID <= 0 {
		return auth.ErrDenied
	}
	var user int64
	if err := s.DB.QueryRow("SELECT user_id FROM web_sessions WHERE id=?", -chatID).Scan(&user); err != nil {
		return err
	}
	if err := auth.Require(s.DB, user, out.WorkshopID, auth.WorkshopRead); err != nil {
		return err
	}
	b, err := json.Marshal(out.Buttons)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO web_messages(session_id,request_id,workshop_id,role,text,buttons,created_at) VALUES(?,?,?,?,?,?,?)`, -chatID, out.RequestID, out.WorkshopID, out.Role, out.Text, string(b), s.Now().Unix())
	return err
}
func (s *Service) History(id int64) ([]message.Output, error) {
	rows, err := s.DB.Query(`SELECT id,role,text,buttons,request_id FROM (SELECT m.id,m.role,m.text,m.buttons,m.request_id FROM web_messages m JOIN web_sessions s ON s.id=m.session_id JOIN workshop_members wm ON wm.user_id=s.user_id AND wm.workshop_id=m.workshop_id JOIN workshops w ON w.id=m.workshop_id JOIN users u ON u.id=s.user_id WHERE m.session_id=? AND wm.status='active' AND wm.is_active=1 AND w.status='active' AND w.is_active=1 AND u.status='active' AND u.is_active=1 AND s.revoked_at IS NULL AND s.expires_at>unixepoch() ORDER BY m.id DESC LIMIT 200) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []message.Output{}
	for rows.Next() {
		var m message.Output
		var buttons string
		if err = rows.Scan(&m.ID, &m.Role, &m.Text, &buttons, &m.RequestID); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(buttons), &m.Buttons); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
