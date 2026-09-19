package llm

import "testing"

func TestWindowFor(t *testing.T) {
	if got := WindowFor("muse-spark"); got != 1000000 {
		t.Fatalf("muse-spark window = %d, want 1000000", got)
	}
	if got := WindowFor("claude-sonnet-4-5-20250929"); got != 200000 {
		t.Fatalf("claude snapshot window = %d, want 200000", got)
	}
	if got := WindowFor("openrouter/gpt-4o-mini"); got != 128000 {
		t.Fatalf("routed window = %d, want 128000", got)
	}
	if got := WindowFor("no-such-model-xyz"); got != DefaultWindow {
		t.Fatalf("unknown window = %d, want %d", got, DefaultWindow)
	}
}

func TestGuard(t *testing.T) {
	// window=100000: warn below 20000 remaining, block below 10000.
	cases := []struct {
		used int
		want string
	}{
		{10000, "ok"},
		{85000, "warn"},  // 15000 remaining
		{95000, "block"}, // 5000 remaining
		{200000, "block"},
	}
	for _, c := range cases {
		if got := Guard(c.used, 100000); got != c.want {
			t.Fatalf("Guard(%d, 100000) = %q, want %q", c.used, got, c.want)
		}
	}
	// Small window: absolute floors dominate (warn<8k, block<4k).
	if got := Guard(5000, 10000); got != "warn" {
		t.Fatalf("Guard(5000, 10000) = %q, want warn", got)
	}
	if got := Guard(9000, 10000); got != "block" {
		t.Fatalf("Guard(9000, 10000) = %q, want block", got)
	}
	if got := Guard(0, 10000); got != "ok" {
		t.Fatalf("Guard(0, 10000) = %q, want ok", got)
	}
}
