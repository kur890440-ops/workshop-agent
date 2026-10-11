package webchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/message"
	"workshop-agent/internal/stability"
)

type Request struct {
	Messages []message.Output `json:"messages,omitempty"`
	ID       string           `json:"request_id"`
	Status   string           `json:"status"`
	Error    string           `json:"error_code,omitempty"`
}

func (s *Service) action(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.session(w, r)
	if !ok {
		return
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if e != nil {
		fail(w, 413, "REQUEST_BODY_TOO_LARGE")
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if !utf8.Valid(body) || d.Decode(&input) != nil {
		fail(w, 400, "INVALID_JSON")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "TRAILING_JSON_DATA")
		return
	}
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		fail(w, 400, "INVALID_IDEMPOTENCY_KEY")
		return
	}
	fingerprint := hash(r.URL.Path + string(body))
	if key != "" {
		var previous, oldHash string
		e := s.DB.QueryRow("SELECT request_id,payload_hash FROM web_requests WHERE session_id=? AND idempotency_key=?", ss.ID, key).Scan(&previous, &oldHash)
		if e == nil {
			if oldHash != fingerprint {
				fail(w, 409, "IDEMPOTENCY_CONFLICT")
				return
			}
			out, e := s.authorizedRequest(ss, previous)
			if e != nil {
				fail(w, 404, "REQUEST_NOT_FOUND")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			jsonReply(w, out)
			return
		}
	}
	confirmation := r.PathValue("id")
	input.Text = strings.TrimSpace(input.Text)
	if confirmation == "" && (input.Text == "" || len([]byte(input.Text)) > 12000) {
		fail(w, 400, "INPUT_LIMIT_EXCEEDED: максимум 12000 UTF-8 байт")
		return
	}
	var workshop int64
	if e = s.DB.QueryRow("SELECT active_workshop_id FROM user_workshop_context WHERE user_id=?", ss.UserID).Scan(&workshop); e != nil || auth.Require(s.DB, ss.UserID, workshop, auth.WorkshopRead) != nil {
		fail(w, 403, "ACCESS_DENIED")
		return
	}
	id, e := random(16)
	if e != nil {
		fail(w, 500, "STORAGE_ERROR")
		return
	}
	in := message.Inbound{InputType: "WEB", UserID: ss.UserID, TelegramUserID: ss.TelegramUserID, WebSessionID: ss.ID, WorkshopID: workshop, ConversationID: -ss.ID, RequestID: id, Text: input.Text, ConfirmationID: confirmation}
	label := input.Text
	if confirmation != "" {
		label = "Действие подтверждения"
	}
	tx, e := s.DB.Begin()
	if e != nil {
		fail(w, 500, "STORAGE_ERROR")
		return
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO web_requests(request_id,idempotency_key,payload_hash,session_id,user_id,workshop_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,'QUEUED',?,?)", id, key, fingerprint, ss.ID, ss.UserID, workshop, s.Now().Unix(), s.Now().Unix()); e == nil {
		_, e = tx.Exec("INSERT INTO web_messages(session_id,request_id,workshop_id,role,text,created_at) VALUES(?,?,?,'user',?,?)", ss.ID, id, workshop, label, s.Now().Unix())
	}
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "STORAGE_ERROR")
		return
	}
	s.jobsMu.Lock()
	job, e := s.Runtime.Submit(s.Root, ss.UserID, -ss.ID, id, confirmation != "", func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var active int
		if e := s.DB.QueryRow("SELECT count(*) FROM web_sessions WHERE id=? AND revoked_at IS NULL AND expires_at>unixepoch()", ss.ID).Scan(&active); e != nil {
			return e
		}
		if active != 1 {
			return auth.ErrDenied
		}
		if e := auth.Require(s.DB, ss.UserID, workshop, auth.WorkshopRead); e != nil {
			return e
		}
		if s.Handle == nil {
			return errors.New("handler unavailable")
		}
		return s.Handle(ctx, in)
	}, func(status string, err error) {
		_, _ = s.DB.Exec("UPDATE web_requests SET status=?,error_code=?,updated_at=? WHERE request_id=?", status, stability.Code(err), s.Now().Unix(), id)
	})
	if e == nil {
		s.jobs[id] = job
	}
	s.jobsMu.Unlock()
	if e != nil {
		_, _ = s.DB.Exec("DELETE FROM web_messages WHERE request_id=?", id)
		_, _ = s.DB.Exec("DELETE FROM web_requests WHERE request_id=?", id)
		var x *stability.Error
		if errors.As(e, &x) && x.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(x.RetryAfter))
		}
		if stability.Code(e) == "QUEUE_FULL" || stability.Code(e) == "USER_QUEUE_FULL" {
			w.Header().Set("Retry-After", "")
		}
		if stability.Code(e) == "QUEUE_FULL" || stability.Code(e) == "USER_QUEUE_FULL" {
			http.Error(w, stability.Code(e), 429)
		} else {
			fail(w, 429, stability.Code(e))
		}
		return
	}
	go func() { job.Wait(); s.jobsMu.Lock(); delete(s.jobs, id); s.jobsMu.Unlock() }()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	jsonReply(w, Request{ID: id, Status: "QUEUED"})
}

// No token is retained by accepted work; revalidate the stored session directly.
func (s *Service) authorizedRequest(ss Session, id string) (Request, error) {
	var out Request
	var workshop int64
	e := s.DB.QueryRow("SELECT request_id,status,error_code,workshop_id FROM web_requests WHERE request_id=? AND session_id=? AND user_id=?", id, ss.ID, ss.UserID).Scan(&out.ID, &out.Status, &out.Error, &workshop)
	if e != nil {
		return out, e
	}
	return out, auth.Require(s.DB, ss.UserID, workshop, auth.WorkshopRead)
}
func (s *Service) requestStatus(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.session(w, r)
	if !ok {
		return
	}
	if !s.allow("requests:"+strconv.FormatInt(ss.ID, 10), 120) {
		fail(w, 429, ErrLimit.Error())
		return
	}
	out, e := s.authorizedRequest(ss, r.PathValue("id"))
	if e != nil {
		fail(w, 404, "REQUEST_NOT_FOUND")
		return
	}
	rows, e := s.History(ss.ID)
	if e != nil {
		fail(w, 500, "STORAGE_ERROR")
		return
	}
	for _, m := range rows {
		if m.RequestID == out.ID {
			out.Messages = append(out.Messages, m)
		}
	}
	jsonReply(w, out)
}
func (s *Service) requests(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.session(w, r)
	if !ok {
		return
	}
	if !s.allow("requests:"+strconv.FormatInt(ss.ID, 10), 120) {
		fail(w, 429, ErrLimit.Error())
		return
	}
	rows, e := s.DB.Query("SELECT request_id FROM web_requests WHERE session_id=? ORDER BY created_at DESC,rowid DESC LIMIT 30", ss.ID)
	if e != nil {
		fail(w, 500, "STORAGE_ERROR")
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e != nil {
		fail(w, 500, "STORAGE_ERROR")
		return
	}
	out := []Request{}
	for _, id := range ids {
		v, e := s.authorizedRequest(ss, id)
		if e == nil {
			out = append(out, v)
		}
	}
	jsonReply(w, out)
}
func (s *Service) cancelRequest(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.session(w, r)
	if !ok {
		return
	}
	if !s.allow("cancel:"+strconv.FormatInt(ss.UserID, 10), 60) {
		fail(w, 429, ErrLimit.Error())
		return
	}
	id := r.PathValue("id")
	out, e := s.authorizedRequest(ss, id)
	if e != nil {
		fail(w, 404, "REQUEST_NOT_FOUND")
		return
	}
	s.jobsMu.Lock()
	j := s.jobs[id]
	if j != nil {
		j.Cancel()
	}
	s.jobsMu.Unlock()
	jsonReply(w, out)
}
