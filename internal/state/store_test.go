package state

import (
	"fmt"
	"sync"
	"testing"
)

func TestOpenSaveTurnSaveFact(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	turn, err := s.SaveTurn("s1", "user", "hello")
	if err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}
	if turn.ID == 0 || turn.Content != "hello" {
		t.Fatalf("unexpected turn: %+v", turn)
	}
	fact, err := s.SaveFact("sky is blue", "test")
	if err != nil {
		t.Fatalf("SaveFact: %v", err)
	}
	if fact.ID == 0 || fact.Text != "sky is blue" {
		t.Fatalf("unexpected fact: %+v", fact)
	}
}

func TestRecentTurnsOrderAndLimit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.SaveTurn("s1", "user", fmt.Sprintf("msg-%d", i)); err != nil {
			t.Fatalf("SaveTurn: %v", err)
		}
	}
	all, err := s.RecentTurns("s1", 0)
	if err != nil {
		t.Fatalf("RecentTurns: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("expected 5 turns, got %d", len(all))
	}
	for i, tr := range all {
		want := fmt.Sprintf("msg-%d", i)
		if tr.Content != want {
			t.Fatalf("order mismatch at %d: got %q want %q", i, tr.Content, want)
		}
	}
	limited, err := s.RecentTurns("s1", 2)
	if err != nil {
		t.Fatalf("RecentTurns: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(limited))
	}
	if limited[0].Content != "msg-3" || limited[1].Content != "msg-4" {
		t.Fatalf("limit mismatch: %+v", limited)
	}
	// Session filter: other session should not leak in.
	if _, err := s.SaveTurn("other", "user", "other-msg"); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}
	s1only, err := s.RecentTurns("s1", 0)
	if err != nil {
		t.Fatalf("RecentTurns: %v", err)
	}
	for _, tr := range s1only {
		if tr.Session != "s1" {
			t.Fatalf("session filter leaked: %+v", tr)
		}
	}
}

func TestAllFactsCount(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.SaveFact(fmt.Sprintf("fact-%d", i), "test"); err != nil {
			t.Fatalf("SaveFact: %v", err)
		}
	}
	facts, err := s.AllFacts()
	if err != nil {
		t.Fatalf("AllFacts: %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts, got %d", len(facts))
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s1.SaveTurn("s1", "user", "persist-me"); err != nil {
		t.Fatalf("SaveTurn: %v", err)
	}
	if _, err := s1.SaveFact("persist-fact", "test"); err != nil {
		t.Fatalf("SaveFact: %v", err)
	}
	// Reopen same dir.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	turns, err := s2.RecentTurns("s1", 0)
	if err != nil {
		t.Fatalf("RecentTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].Content != "persist-me" {
		t.Fatalf("turns not persisted: %+v", turns)
	}
	facts, err := s2.AllFacts()
	if err != nil {
		t.Fatalf("AllFacts: %v", err)
	}
	if len(facts) != 1 || facts[0].Text != "persist-fact" {
		t.Fatalf("facts not persisted: %+v", facts)
	}
}

func TestConcurrentSaveTurn(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.SaveTurn("conc", "user", fmt.Sprintf("msg-%d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SaveTurn concurrent: %v", err)
		}
	}
	turns, err := s.RecentTurns("conc", 0)
	if err != nil {
		t.Fatalf("RecentTurns: %v", err)
	}
	if len(turns) != n {
		t.Fatalf("expected %d turns, got %d", n, len(turns))
	}
}
