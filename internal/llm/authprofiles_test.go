package llm

import (
	"testing"
	"time"
)

func testStore() *Store {
	return NewStore(
		&Profile{ID: "openai/a", Provider: "openai", Key: "k-a"},
		&Profile{ID: "openai/b", Provider: "openai", Key: "k-b"},
		&Profile{ID: "groq/g", Provider: "groq", Key: "k-g"},
	)
}

func orderIDs(ps []*Profile) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestOrder_EligibleFirstStable(t *testing.T) {
	s := testStore()
	got := orderIDs(s.Order("openai"))
	if len(got) != 2 || got[0] != "openai/a" || got[1] != "openai/b" {
		t.Fatalf("order = %v, want [openai/a openai/b]", got)
	}
	// Cooling profile sinks below eligible ones, stably.
	s.ReportFailure("openai/a", false)
	got = orderIDs(s.Order("openai"))
	if len(got) != 2 || got[0] != "openai/b" || got[1] != "openai/a" {
		t.Fatalf("order after cooldown = %v, want [openai/b openai/a]", got)
	}
}

func TestReportFailure_TerminalExiles(t *testing.T) {
	s := testStore()
	s.ReportFailure("openai/a", true)
	got := orderIDs(s.Order("openai"))
	if len(got) != 2 || got[0] != "openai/b" || got[1] != "openai/a" {
		t.Fatalf("order after exile = %v, want [openai/b openai/a]", got)
	}
	p := s.find("openai/a")
	if p == nil || !p.Exiled {
		t.Fatalf("profile a should be exiled")
	}
	// Non-terminal failure: 60s cooldown + fails++.
	s2 := testStore()
	s2.ReportFailure("openai/b", false)
	pb := s2.find("openai/b")
	if pb.Fails != 1 {
		t.Fatalf("fails = %d, want 1", pb.Fails)
	}
	if time.Until(pb.CooldownUntil) < 50*time.Second {
		t.Fatalf("cooldown too short: %v", time.Until(pb.CooldownUntil))
	}
	s2.ReportSuccess("openai/b")
	if pb.Fails != 0 || !pb.CooldownUntil.IsZero() {
		t.Fatalf("after success: fails=%d cooldown=%v", pb.Fails, pb.CooldownUntil)
	}
}

func TestPin_Unpin(t *testing.T) {
	s := testStore()
	s.ReportFailure("openai/a", false) // a cools, b leads
	s.Pin("openai/a")                  // user lock overrides
	got := orderIDs(s.Order("openai"))
	if got[0] != "openai/a" {
		t.Fatalf("pinned order = %v, want openai/a first", got)
	}
	s.Unpin()
	got = orderIDs(s.Order("openai"))
	if got[0] != "openai/b" {
		t.Fatalf("unpinned order = %v, want openai/b first", got)
	}
}

func TestFromEnv_StableOrder(t *testing.T) {
	t.Setenv("NB_MULTI_ONE", "k1")
	t.Setenv("NB_MULTI_TWO", "k1") // same material → deduped
	t.Setenv("NB_MULTI_ZERO", "k2")
	t.Setenv("OPENAI_API_KEY", "base")
	s := FromEnv("openai", []string{"NB_MULTI_"})
	got := orderIDs(s.Order("openai"))
	want := []string{"openai/nb_multi_one", "openai/nb_multi_zero", "openai/openai_api_key"}
	if len(got) != len(want) {
		t.Fatalf("fromEnv order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fromEnv order = %v, want %v", got, want)
		}
	}
}
