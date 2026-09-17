// Package cli implements local operator diagnostics. It never prints invite hashes or tokens.
package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"workshop-agent/internal/workshops"
)

func IsCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "users", "workshops", "workshop", "authorization-trace", "memory":
		return true
	}
	return false
}
func Run(path string, args []string, out io.Writer) error {
	s := workshops.NewService(path)
	defer s.Close()
	if len(args) > 0 && args[0] == "memory" {
		return runMemory(s, args, out)
	}
	var query string
	var values []any
	id := func(value string) (int64, error) {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid internal ID: %q", value)
		}
		return n, nil
	}
	if len(args) == 2 && args[1] == "list" {
		switch args[0] {
		case "users":
			query = `SELECT id,telegram_user_id,telegram_username,first_name,last_name,status FROM users ORDER BY id`
		case "workshops":
			query = `SELECT id,name,status FROM workshops ORDER BY id`
		}
	} else if (len(args) == 3 && args[0] == "authorization-trace") || (len(args) == 4 && args[0] == "workshop" && args[1] == "permissions") {
		tail := args[len(args)-2:]
		user, err := id(tail[0])
		if err != nil {
			return err
		}
		workshop, err := id(tail[1])
		if err != nil {
			return err
		}
		trace, err := s.PermissionTrace(user, workshop)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(trace)
	} else if len(args) == 3 && args[0] == "workshop" {
		workshop, err := id(args[2])
		if err != nil {
			return err
		}
		values = []any{workshop}
		switch args[1] {
		case "members":
			query = `SELECT m.id,m.user_id,u.display_name,m.role,m.status,m.joined_at FROM workshop_members m JOIN users u ON u.id=m.user_id WHERE m.workshop_id=? ORDER BY m.id`
		case "invites":
			query = `SELECT id,workshop_id,created_by_user_id,role,CASE WHEN status='active' AND expires_at<=unixepoch() THEN 'expired' ELSE status END AS status,expires_at,max_uses,used_count,created_at,revoked_at FROM workshop_invites WHERE workshop_id=? ORDER BY id`
		case "audit":
			query = `SELECT id,workshop_id,actor_user_id,target_user_id,event_type,metadata_json,action,created_at FROM audit_logs WHERE workshop_id=? ORDER BY id DESC`
		}
	}
	if query == "" {
		return fmt.Errorf("usage: users list | workshops list | workshop members/invites/audit <id> | workshop permissions <user> <workshop> | authorization-trace <user> <workshop>")
	}
	return printRows(s.DB(), out, query, values...)
}
func printRows(db *sql.DB, out io.Writer, query string, args ...any) error {
	rows, err := db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	list := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range ptrs {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		item := map[string]any{}
		for i, c := range columns {
			item[c] = values[i]
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}
