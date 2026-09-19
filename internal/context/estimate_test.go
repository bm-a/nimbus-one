package context

import (
	"strings"
	"testing"
)

func TestEstimateText(t *testing.T) {
	if EstimateText("") != 0 {
		t.Fatal("empty = 0")
	}
	if got := EstimateText("abcd"); got != 2 { // 1 + framing? no: 4/4+1
		t.Fatalf("abcd = %d, want 2", got)
	}
	// Longer text must exceed its char-quarter (conservative direction).
	if got := EstimateText(strings.Repeat("x", 1000)); got < 250 {
		t.Fatalf("underestimates: %d", got)
	}
}

func TestBudgetStates(t *testing.T) {
	b := Budget{Window: 1000}
	cases := map[int]string{100: "ok", 540: "prune", 800: "compact", 950: "over"}
	for used, want := range cases {
		if got := b.State(used); got != want {
			t.Fatalf("State(%d) = %q, want %q", used, got, want)
		}
	}
	if got := (Budget{}).State(999999); got != "ok" {
		t.Fatalf("unknown window must be ok, got %q", got)
	}
}
