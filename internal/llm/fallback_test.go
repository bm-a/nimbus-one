package llm

import (
	"context"
	"errors"
	"testing"
)

type stubProvider struct {
	name  string
	text  string
	tcs   []ToolCall
	err   error
	calls int
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Complete(ctx context.Context, req ChatRequest) (string, []ToolCall, error) {
	s.calls++
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return s.text, s.tcs, s.err
}

func (s *stubProvider) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if s.err != nil {
		return nil, s.err
	}
	ch := make(chan Chunk, 2)
	ch <- Chunk{Delta: s.text}
	ch <- Chunk{ToolCalls: s.tcs, Done: true}
	close(ch)
	return ch, nil
}

func TestChain_FallsOverOn429(t *testing.T) {
	p1 := &stubProvider{name: "p1", err: &StatusError{Code: 429, Body: "rate limited"}}
	p2 := &stubProvider{name: "p2", text: "ok"}
	c := NewChain(p1, p2)

	text, _, err := c.Complete(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "ok" {
		t.Fatalf("text = %q, want %q", text, "ok")
	}
	if p1.calls != 1 || p2.calls != 1 {
		t.Fatalf("calls = (%d,%d), want (1,1)", p1.calls, p2.calls)
	}
	le := c.LastErr()
	if le == nil {
		t.Fatal("LastErr = nil, want first provider's 429")
	}
	var se *StatusError
	if !errors.As(le, &se) || se.Code != 429 {
		t.Fatalf("LastErr = %v, want StatusError 429", le)
	}
}

func TestChain_NonRetryableDoesNotFallOver(t *testing.T) {
	p1 := &stubProvider{name: "p1", err: StatusError{Code: 401, Body: "bad key"}}
	p2 := &stubProvider{name: "p2", text: "ok"}
	c := NewChain(p1, p2)

	_, _, err := c.Complete(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var pse *StatusError
	var vse StatusError
	code := 0
	switch {
	case errors.As(err, &pse):
		code = pse.Code
	case errors.As(err, &vse):
		code = vse.Code
	}
	if code != 401 {
		t.Fatalf("err = %v (%T), want StatusError 401", err, err)
	}
	if p2.calls != 0 {
		t.Fatalf("p2.calls = %d, want 0 (no failover on 401)", p2.calls)
	}
	if c.LastErr() == nil {
		t.Fatal("LastErr = nil, want 401")
	}
}

func TestChain_AllFailReturnsWrapped(t *testing.T) {
	p1 := &stubProvider{name: "p1", err: &StatusError{Code: 503, Body: "down"}}
	p2 := &stubProvider{name: "p2", err: &StatusError{Code: 503, Body: "down"}}
	c := NewChain(p1, p2)

	_, _, err := c.Complete(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if c.LastErr() == nil {
		t.Fatal("LastErr = nil, want last failure")
	}
}

func TestChain_EmptyReturnsError(t *testing.T) {
	c := NewChain()
	_, _, err := c.Complete(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
