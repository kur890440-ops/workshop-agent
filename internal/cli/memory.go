package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"workshop-agent/internal/memory"
	"workshop-agent/internal/workshops"
)

func runMemory(ws *workshops.Service, args []string, out io.Writer) error {
	if len(args) < 4 {
		return fmt.Errorf("usage: memory show|trace|list|working|long-term <user_id> <workshop_id> [session_id] [query]")
	}
	user, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return err
	}
	workshop, err := strconv.ParseInt(args[3], 10, 64)
	if err != nil {
		return err
	}
	sc := memory.Scope{UserID: user, WorkshopID: workshop}
	if len(args) > 4 {
		sc.SessionID, err = strconv.ParseInt(args[4], 10, 64)
	} else {
		err = ws.DB().QueryRow(`SELECT id FROM conversation_sessions WHERE user_id=? AND workshop_id=? AND status='active' ORDER BY id DESC LIMIT 1`, user, workshop).Scan(&sc.SessionID)
	}
	if err != nil {
		return fmt.Errorf("active session required: %w", err)
	}
	query := "память"
	if len(args) > 5 {
		query = strings.Join(args[5:], " ")
	}
	m := memory.New(ws.DB()).ForUser(user)
	var result any
	switch args[1] {
	case "trace":
		result, err = m.LastTrace(sc)
	case "working":
		result, err = m.ActiveWorking(sc)
	case "list", "long-term":
		task, e := m.ActiveWorking(sc)
		if e != nil {
			return e
		}
		kind := ""
		product := int64(0)
		if task != nil {
			kind = task.Type
			product = task.State.ProductID
		}
		result, err = m.RelevantLongTerm(sc, query, kind, product)
	case "show":
		result, err = (memory.AgentContextBuilder{Memory: m}).Build(sc, query, nil, memory.All)
	default:
		return fmt.Errorf("unknown memory command")
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
