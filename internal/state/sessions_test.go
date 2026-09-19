package state

import (
	"errors"
	"testing"
)

func openTestSessions(t *testing.T) *SessionStore {
	t.Helper()
	s, err := OpenSessions(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseSessionKey(t *testing.T) {
	id, rest, err := ParseSessionKey("agent:worker-1:main")
	if err != nil {
		t.Fatal(err)
	}
	if id != "worker-1" || rest != "main" {
		t.Fatalf("got %q %q", id, rest)
	}
	if !IsMainSession("agent:worker-1:main") {
		t.Fatal("main session not detected")
	}
	if IsMainSession("agent:worker-1:branch-a") {
		t.Fatal("branch misdetected as main")
	}
	for _, bad := range []string{"", "agent::main", "agent:x:", "user:x:main", "agent:x", "agent"} {
		if _, _, err := ParseSessionKey(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
		if IsMainSession(bad) {
			t.Fatalf("IsMainSession true for %q", bad)
		}
	}
	// 4-part keys are invalid (SplitN keeps the tail, which still must be non-empty;
	// extra colons land in <rest> — verify a colon-bearing rest is accepted as opaque).
	if _, rest, err := ParseSessionKey("agent:x:a:b"); err != nil || rest != "a:b" {
		t.Fatalf("colon rest: %v %q", err, rest)
	}
}

func TestSessionCreateGet(t *testing.T) {
	s := openTestSessions(t)
	sess, err := s.Create("agent:a:main", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if sess.AgentID != "a" || sess.LifecycleRev != 1 || sess.Archived {
		t.Fatalf("bad record: %+v", sess)
	}
	got, err := s.Get("agent:a:main")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != sess.Key || got.AgentID != "a" {
		t.Fatalf("bad get: %+v", got)
	}
	if _, err := s.Get("agent:a:nope"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
	if _, err := s.Create("agent:a:main", "", 0); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("want ErrSessionExists, got %v", err)
	}
	if _, err := s.Create("bogus", "", 0); err == nil {
		t.Fatal("expected invalid-key error")
	}
}

func TestSessionAppendRecent(t *testing.T) {
	s := openTestSessions(t)
	if _, err := s.Create("agent:a:main", "", 0); err != nil {
		t.Fatal(err)
	}
	for i, c := range []string{"one", "two", "three"} {
		ev, err := s.Append("agent:a:main", "user", c)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Seq != int64(i+1) {
			t.Fatalf("want seq %d, got %d", i+1, ev.Seq)
		}
	}
	all, err := s.Recent("agent:a:main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Content != "one" || all[2].Content != "three" {
		t.Fatalf("bad order: %+v", all)
	}
	last, err := s.Recent("agent:a:main", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 || last[0].Content != "two" {
		t.Fatalf("bad tail: %+v", last)
	}
	if _, err := s.Append("agent:a:missing", "user", "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestSessionFencing(t *testing.T) {
	s := openTestSessions(t)
	sess, err := s.Create("agent:a:main", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAtRev("agent:a:main", "user", "ok", sess.LifecycleRev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAtRev("agent:a:main", "user", "stale", sess.LifecycleRev-1); err == nil {
		t.Fatal("expected revision mismatch")
	} else if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("want ErrRevisionMismatch, got %v", err)
	}
	// Failed fenced write must not append.
	evs, err := s.Recent("agent:a:main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("fencing failure wrote anyway: %+v", evs)
	}
}

func TestSessionArchiveFreezes(t *testing.T) {
	s := openTestSessions(t)
	sess, err := s.Create("agent:a:main", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Archive("agent:a:main"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("agent:a:main")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Archived || got.LifecycleRev != sess.LifecycleRev+1 {
		t.Fatalf("bad archive: %+v", got)
	}
	// Pre-archive revision is now stale.
	if _, err := s.AppendAtRev("agent:a:main", "user", "late", sess.LifecycleRev); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("want ErrRevisionMismatch, got %v", err)
	}
	// Plain appends are refused too.
	if _, err := s.Append("agent:a:main", "user", "late"); !errors.Is(err, ErrSessionArchived) {
		t.Fatalf("want ErrSessionArchived, got %v", err)
	}
	if err := s.Archive("agent:a:missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestSessionForkIsolation(t *testing.T) {
	s := openTestSessions(t)
	if _, err := s.Create("agent:a:main", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("agent:a:main", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("agent:a:main", "assistant", "world"); err != nil {
		t.Fatal(err)
	}
	child, err := s.Fork("agent:a:main", "agent:a:branch-1")
	if err != nil {
		t.Fatal(err)
	}
	if child.SpawnDepth != 1 || child.SpawnedBy != "agent:a:main" || child.LifecycleRev != 1 {
		t.Fatalf("bad child: %+v", child)
	}
	cevs, err := s.Recent("agent:a:branch-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cevs) != 2 || cevs[0].Content != "hello" || cevs[0].Seq != 1 || cevs[1].Seq != 2 {
		t.Fatalf("bad copied transcript: %+v", cevs)
	}
	// Isolation both directions.
	if _, err := s.Append("agent:a:branch-1", "user", "child-only"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("agent:a:main", "user", "parent-only"); err != nil {
		t.Fatal(err)
	}
	pevs, _ := s.Recent("agent:a:main", 0)
	cevs, _ = s.Recent("agent:a:branch-1", 0)
	if len(pevs) != 3 || pevs[2].Content != "parent-only" {
		t.Fatalf("parent polluted: %+v", pevs)
	}
	if len(cevs) != 3 || cevs[2].Content != "child-only" {
		t.Fatalf("child polluted: %+v", cevs)
	}
	if _, err := s.Fork("agent:a:main", "agent:a:branch-1"); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("want ErrSessionExists, got %v", err)
	}
	if _, err := s.Fork("agent:a:missing", "agent:a:branch-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}
