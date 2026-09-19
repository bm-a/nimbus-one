package llm

import "testing"

func TestNormalize_OpenAI(t *testing.T) {
	u := Normalize("openai", map[string]any{
		"prompt_tokens":     float64(100),
		"completion_tokens": float64(50),
		"total_tokens":      float64(150),
		"prompt_tokens_details": map[string]any{
			"cached_tokens": float64(20),
		},
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": float64(7),
		},
	})
	if u.Input != 80 || u.Output != 50 || u.CacheRead != 20 || u.Reasoning != 7 {
		t.Fatalf("openai usage = %+v, want input=80 output=50 cacheRead=20 reasoning=7", u)
	}
	if u.CacheWrite != 0 {
		t.Fatalf("openai cacheWrite = %d, want 0", u.CacheWrite)
	}
}

func TestNormalize_Anthropic(t *testing.T) {
	u := Normalize("anthropic", map[string]any{
		"input_tokens":                100,
		"output_tokens":               50,
		"cache_read_input_tokens":     10,
		"cache_creation_input_tokens": 5,
	})
	if u.Input != 100 || u.Output != 50 || u.CacheRead != 10 || u.CacheWrite != 5 {
		t.Fatalf("anthropic usage = %+v, want 100/50/10/5", u)
	}
}

func TestNormalize_LlamaCPP(t *testing.T) {
	u := Normalize("llama.cpp", map[string]any{
		"prompt_n":    30,
		"predicted_n": 12,
	})
	if u.Input != 30 || u.Output != 12 {
		t.Fatalf("llama.cpp usage = %+v, want input=30 output=12", u)
	}
	// Nested timings shape streamed by llama.cpp.
	u2 := Normalize("ollama", map[string]any{
		"timings": map[string]any{"prompt_n": 8, "predicted_n": 3},
	})
	if u2.Input != 8 || u2.Output != 3 {
		t.Fatalf("llama timings usage = %+v, want input=8 output=3", u2)
	}
}

func TestNormalize_DefaultZeros(t *testing.T) {
	u := Normalize("mystery-provider", map[string]any{"prompt_tokens": 10})
	if !u.IsZero() {
		t.Fatalf("unknown provider usage = %+v, want zeros", u)
	}
	if u := Normalize("openai", nil); !u.IsZero() {
		t.Fatalf("nil raw usage = %+v, want zeros", u)
	}
}

func TestUsage_AddTotal(t *testing.T) {
	a := Usage{Input: 10, Output: 5, CacheRead: 3, CacheWrite: 2, Reasoning: 4}
	b := Usage{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1, Reasoning: 1}
	s := a.Add(b)
	if s.Input != 11 || s.Output != 6 || s.CacheRead != 4 || s.CacheWrite != 3 || s.Reasoning != 5 {
		t.Fatalf("Add = %+v", s)
	}
	// Reasoning is a subset of Output: excluded from Total.
	if got, want := s.Total(), int64(11+6+4+3); got != want {
		t.Fatalf("Total = %d, want %d", got, want)
	}
}

func TestSessionUsage(t *testing.T) {
	var s SessionUsage
	s.Add(Usage{Input: 10, Output: 5})
	s.Add(Usage{Input: 7, CacheRead: 3})
	snap := s.Snapshot()
	if snap.Input != 17 || snap.Output != 5 || snap.CacheRead != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if s.Calls() != 2 {
		t.Fatalf("calls = %d, want 2", s.Calls())
	}
	s.Reset()
	if !s.Snapshot().IsZero() || s.Calls() != 0 {
		t.Fatalf("after reset: %+v calls=%d", s.Snapshot(), s.Calls())
	}
}
