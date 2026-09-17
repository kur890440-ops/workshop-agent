package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"workshop-agent/internal/auth"
	"workshop-agent/internal/workshops"
)

func TestDiagnosticsDoNotExposeInviteSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.db")
	s := workshops.NewService(path)
	defer s.Close()
	u, err := s.UpsertUser(123456, "", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateOwnedWorkshop(u, "A")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.CreateInvite(u, w, auth.Employee, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"users", "list"}, {"workshops", "list"}, {"workshop", "members", "1"}, {"workshop", "invites", "1"}, {"workshop", "audit", "1"}, {"workshop", "permissions", "1", "1"}, {"authorization-trace", "1", "1"}} {
		var buf bytes.Buffer
		if err := Run(path, args, &buf); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), token) || strings.Contains(buf.String(), "token_hash") {
			t.Fatal("secret in CLI output")
		}
	}
	var buf bytes.Buffer
	if err := Run(path, []string{"workshop", "invites", "1 OR 1=1"}, &buf); err == nil {
		t.Fatal("invalid ID accepted")
	}
}
