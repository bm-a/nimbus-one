package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(srv *httptest.Server) *Client {
	return &Client{APIKey: "k", BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
}

func TestComplete_NonStreamReturnsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": "hello world",
					},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	text, _, err := c.Complete(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
}

func TestComplete_StreamSSEParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"hello "}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"world"}}]}`)
		fmt.Fprintln(w, ``)
		fmt.Fprintln(w, `: keepalive comment`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	text, _, err := c.Complete(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
}

func TestChat_StreamSSEParsingChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"a"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"b"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ch, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var sb strings.Builder
	var done bool
	for ck := range ch {
		if ck.Err != nil {
			t.Fatalf("chunk err: %v", ck.Err)
		}
		sb.WriteString(ck.Delta)
		if ck.Done {
			done = true
		}
	}
	if sb.String() != "ab" {
		t.Fatalf("streamed = %q, want %q", sb.String(), "ab")
	}
	if !done {
		t.Fatal("want terminal Done chunk")
	}
}

func TestComplete_429ReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, _, err := c.Complete(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err type = %T, want *StatusError", err)
	}
	if se.Code != 429 {
		t.Fatalf("code = %d, want 429", se.Code)
	}
}

func TestChat_429ReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("slow down"))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err type = %T, want *StatusError", err)
	}
	if se.Code != http.StatusTooManyRequests {
		t.Fatalf("code = %d, want 429", se.Code)
	}
}

func TestComplete_ContextCancelPropagates(t *testing.T) {
	// Server that would block; cancelled ctx must win without waiting.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.Complete(ctx, ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	_, err = c.Chat(ctx, ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("want Chat error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Chat err = %v, want context.Canceled", err)
	}
}
