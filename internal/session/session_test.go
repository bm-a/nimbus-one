package session

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSession_RoundTrip(t *testing.T) {
	s := openTest(t)
	id, err := s.CreateSession("", "demo", "/work", "model-x")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.AppendMessage(id, RoleUser, "hello", "", ""); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := s.AppendMessage(id, RoleAssistant, "hi", "", ""); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := s.AppendMessage(id, RoleTool, "out", "bash", "call-1"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	h, err := s.History(id)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(h) != 3 {
		t.Fatalf("history len = %d, want 3", len(h))
	}
	if h[2].Name != "bash" || h[2].ToolCallID != "call-1" {
		t.Fatalf("tool row = %+v, want name/tool_call_id preserved", h[2])
	}
}

func TestSession_BadRole(t *testing.T) {
	s := openTest(t)
	id, _ := s.CreateSession("x", "", "", "")
	if _, err := s.AppendMessage(id, "bogus", "c", "", ""); err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestSession_Usage(t *testing.T) {
	s := openTest(t)
	id, _ := s.CreateSession("u1", "", "", "")
	if _, err := s.GetUsage(id); err != nil {
		t.Fatalf("GetUsage empty: %v", err)
	}
	if err := s.RecordUsage(id, 100, 50, 7); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if err := s.RecordUsage(id, 100, 50, 7); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	u, err := s.GetUsage(id)
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if u.InputTokens != 200 || u.OutputTokens != 100 || u.CostCents != 14 {
		t.Fatalf("usage = %+v, want accumulated", u)
	}
}

func TestSession_ListNewestFirst(t *testing.T) {
	s := openTest(t)
	if _, err := s.CreateSession("a", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession("b", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage("b", RoleUser, "touch", "", ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSessions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "b" {
		t.Fatalf("list order = %+v, want b first", list)
	}
}

func TestSession_Reopen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.db")
	s1, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.CreateSession("r", "n", "", ""); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	list, err := s2.ListSessions(10)
	if err != nil || len(list) != 1 || list[0].ID != "r" {
		t.Fatalf("after reopen list = %+v, err = %v", list, err)
	}
}
