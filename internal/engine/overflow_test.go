package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/llm/pool"
	"nimbus-one/internal/tools"
)

func TestCompactOldestKeepsSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "one"},
		{Role: llm.RoleAssistant, Content: "two"},
		{Role: llm.RoleUser, Content: "three"},
		{Role: llm.RoleAssistant, Content: "four"},
	}
	got := compactOldest(msgs, 50)
	if got[0].Content != "sys" {
		t.Fatal("system prompt must survive")
	}
	if len(got) >= len(msgs) {
		t.Fatal("should shrink")
	}
	found := false
	for _, m := range got {
		if strings.Contains(m.Content, "auto-compact") {
			found = true
		}
	}
	if !found {
		t.Fatal("summary marker missing")
	}
	// newest turn survives
	if got[len(got)-1].Content != "four" {
		t.Fatal("newest turn must survive")
	}
}

type overflowThenOK struct {
	n int
}

func (o *overflowThenOK) Name() string { return "overflow" }
func (o *overflowThenOK) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 1)
	close(ch)
	return ch, errors.New("unused")
}

// Call 1 requests a tool (builds multi-turn history), call 2 overflows,
// call 3 recovers after compaction.
func (o *overflowThenOK) Complete(ctx context.Context, req llm.ChatRequest) (string, []llm.ToolCall, error) {
	o.n++
	switch o.n {
	case 1:
		return "working", []llm.ToolCall{{ID: "1", Name: "echo", Arguments: `{"text":"hi"}`}}, nil
	case 2:
		return "", nil, &pool.ContextOverflowError{Msg: "context_length_exceeded"}
	default:
		return "recovered", nil, nil
	}
}

type echoTool struct{}

func (echoTool) Name() string                       { return "echo" }
func (echoTool) Description() string                { return "echo" }
func (echoTool) Parameters() map[string]tools.Param { return map[string]tools.Param{} }
func (e echoTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	return "echo-out", nil
}

func TestRunHealsContextOverflow(t *testing.T) {
	p := &overflowThenOK{}
	reg := tools.NewRegistry()
	reg.Register(echoTool{})
	e := &Engine{LLM: p, Tools: reg, MaxSteps: 5}
	text, err := e.Run(context.Background(), "sys", "do it")
	if err != nil {
		t.Fatalf("should heal and resend: %v", err)
	}
	if text != "recovered" {
		t.Fatalf("got %q", text)
	}
	if p.n != 3 {
		t.Fatalf("expected 3 calls, got %d", p.n)
	}
}
