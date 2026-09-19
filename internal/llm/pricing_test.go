package llm

import "testing"

func TestCost_KnownModel(t *testing.T) {
	// gpt-4o-mini: $0.15/$0.60 per 1k → 1000 in + 500 out = $0.45.
	got := Cost("gpt-4o-mini", Usage{Input: 1000, Output: 500})
	if got < 0.44999 || got > 0.45001 {
		t.Fatalf("cost = %v, want 0.45", got)
	}
}

func TestCost_UnknownZero(t *testing.T) {
	if got := Cost("definitely-not-a-model-xyz", Usage{Input: 100000, Output: 50000}); got != 0 {
		t.Fatalf("unknown model cost = %v, want 0", got)
	}
	if got := Cost("", Usage{Input: 10}); got != 0 {
		t.Fatalf("empty model cost = %v, want 0", got)
	}
}

func TestCost_PrefixAndRoutingForms(t *testing.T) {
	base := Cost("gpt-4o-mini", Usage{Input: 1000})
	for _, alias := range []string{
		"GPT-4O-MINI",
		"openrouter/gpt-4o-mini",
		"gpt-4o-mini-2024-07-18",
		"openai/gpt-4o-mini:nitro",
	} {
		if got := Cost(alias, Usage{Input: 1000}); got != base || got == 0 {
			t.Fatalf("alias %q cost = %v, want %v", alias, got, base)
		}
	}
	// Dated Claude snapshot resolves through the family prefix.
	if got := Cost("claude-sonnet-4-5-20250929", Usage{Input: 1000}); got != 3.00 {
		t.Fatalf("claude snapshot cost = %v, want 3.00", got)
	}
}
