package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSuggestedModelsStartsWithMuse(t *testing.T) {
	if SuggestedModels[0] != "meta/muse-spark-1.3-contributor" {
		t.Fatalf("chain must start with Muse Spark: %v", SuggestedModels)
	}
}

func TestListModelsFallbackOffline(t *testing.T) {
	// Unreachable URL can't be injected (const), so test fallbackModels directly
	// plus a bad-ping path below. Here: context cancel forces error path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	models, err := ListOpenRouterModels(ctx)
	if err == nil {
		t.Fatal("cancelled ctx should error")
	}
	if len(models) == 0 {
		t.Fatal("must still return fallback models offline")
	}
}

func TestPingRejects401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	if _, err := pingOpenRouterURL(context.Background(), "bad-key", srv.URL); err == nil {
		t.Fatal("401 must be rejected")
	} else if !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("wrong error: %v", err)
	}
	if _, err := PingOpenRouter(context.Background(), ""); err == nil {
		t.Fatal("empty key must fail")
	}
}

func TestPingRateLimitedPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()
	model, err := pingOpenRouterURL(context.Background(), "good-key", srv.URL)
	if err != nil {
		t.Fatalf("429 should still validate: %v", err)
	}
	if model != SuggestedModels[0] {
		t.Fatalf("429 should yield default model, got %q", model)
	}
}

func TestPingPicksMuseWhenListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"some/other"},{"id":"meta/muse-spark-1.3-contributor"}]}`))
	}))
	defer srv.Close()
	model, err := pingOpenRouterURL(context.Background(), "good-key", srv.URL)
	if err != nil || model != "meta/muse-spark-1.3-contributor" {
		t.Fatalf("should prefer Muse (model=%q err=%v)", model, err)
	}
}

func TestOpenRouterChain(t *testing.T) {
	chain := OpenRouterChain("k", []string{"m1", "", "m2"})
	if len(chain) != 2 {
		t.Fatalf("chain len %d (want 2, blanks skipped)", len(chain))
	}
	for _, p := range chain {
		if p.Name() == "" {
			t.Fatal("provider needs a name")
		}
	}
	if len(OpenRouterChain("k", nil)) != 0 {
		t.Fatal("empty in must mean empty out — never silent defaults")
	}
}
