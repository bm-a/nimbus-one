package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func drain(t *testing.T, ch <-chan Chunk) (last Chunk, deltas string) {
	t.Helper()
	for ck := range ch {
		if ck.Err != nil {
			t.Fatalf("chunk err: %v", ck.Err)
		}
		deltas += ck.Delta
		last = ck
	}
	return last, deltas
}

func TestChat_NonStreamUsageCapture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{
					"role": "assistant", "content": "hi there",
				}},
			},
			"usage": map[string]any{
				"prompt_tokens":     float64(100),
				"completion_tokens": float64(50),
				"prompt_tokens_details": map[string]any{
					"cached_tokens": float64(20),
				},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ch, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	last, deltas := drain(t, ch)
	if deltas != "hi there" {
		t.Fatalf("deltas = %q", deltas)
	}
	if last.Usage.Input != 80 || last.Usage.Output != 50 || last.Usage.CacheRead != 20 {
		t.Fatalf("usage = %+v, want input=80 output=50 cacheRead=20", last.Usage)
	}
	if last.RawTokens != 0 {
		t.Fatalf("RawTokens = %d, want 0 when usage present", last.RawTokens)
	}
}

func TestChat_StreamUsageCapture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hey\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n"))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ch, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	last, _ := drain(t, ch)
	if !last.Done {
		t.Fatalf("last chunk not Done: %+v", last)
	}
	if last.Usage.Input != 10 || last.Usage.Output != 4 {
		t.Fatalf("usage = %+v, want input=10 output=4", last.Usage)
	}
}

func TestChat_NoUsageKeepsRawTokensHeuristic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{
					"role": "assistant", "content": "12345678",
				}},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ch, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var sawHeuristic bool
	for ck := range ch {
		if ck.Err != nil {
			t.Fatalf("chunk err: %v", ck.Err)
		}
		if ck.Delta != "" && ck.Usage.IsZero() && ck.RawTokens == 2 {
			sawHeuristic = true
		}
	}
	if !sawHeuristic {
		t.Fatalf("expected RawTokens heuristic chunk when usage absent")
	}
}
