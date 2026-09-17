package testkit

import (
	"testing"
	"workshop-agent/internal/workshops"
)

func Owner(t testing.TB, path string) (int64, int64) {
	t.Helper()
	s := workshops.NewService(path)
	defer s.Close()
	user, err := s.UpsertUser(900001, "owner", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	workshop, err := s.CreateOwnedWorkshop(user, "Test workshop")
	if err != nil {
		t.Fatal(err)
	}
	return user, workshop
}
