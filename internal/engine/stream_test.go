package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/tools"
)

type streamTurn struct {
	deltas []string
	calls  []llm.ToolCall
}

type streamScriptProvider struct {
	mu    sync.Mutex
	turns []streamTurn
	used  int
}

func (p *streamScriptProvider) Name() string { return "stream-fake" }

func (p *streamScriptProvider) Chat(_ context.Context, _ llm.ChatRequest) (<-chan llm.Chunk, error) {
	p.mu.Lock()
	i := p.used
	if i >= len(p.turns) {
		i = len(p.turns) - 1
	}
	p.used++
	turn := p.turns[i]
	p.mu.Unlock()
	ch := make(chan llm.Chunk, len(turn.deltas)+1)
	go func() {
		defer close(ch)
		for _, d := range turn.deltas {
			ch <- llm.Chunk{Delta: d}
		}
		ch <- llm.Chunk{ToolCalls: turn.calls, Done: true}
	}()
	return ch, nil
}

func (p *streamScriptProvider) Complete(_ context.Context, _ llm.ChatRequest) (string, []llm.ToolCall, error) {
	return "", nil, fmt.Errorf("stream-fake: Complete not implemented")
}

type streamEchoTool struct{ ran *int32 }

func (t *streamEchoTool) Name() string { return "echo2" }

func (t *streamEchoTool) Description() string { return "echo" }

func (t *streamEchoTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{"text": {Type: "string"}}
}

func (t *streamEchoTool) Execute(_ context.Context, args map[string]any) (string, error) {
	s, _ := args["text"].(string)
	return "ECHO:" + s, nil
}

func TestRunStream_ForwardsDeltasAndRunsTools(t *testing.T) {
	prov := &streamScriptProvider{turns: []streamTurn{
		{deltas: []string{"hel", "lo "}, calls: []llm.ToolCall{{ID: "1", Name: "echo2", Arguments: `{"text":"abc"}`}}},
		{deltas: []string{"BYE"}},
	}}
	reg := tools.NewRegistry()
	reg.Register(&streamEchoTool{})
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5}

	var mu sync.Mutex
	var emitted []string
	var doneSeen bool
	out, err := e.RunStream(context.Background(), "sys", "go", func(ck llm.Chunk) {
		mu.Lock()
		defer mu.Unlock()
		if ck.Delta != "" {
			emitted = append(emitted, ck.Delta)
		}
		if ck.Done {
			doneSeen = true
		}
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if out != "BYE" {
		t.Fatalf("out = %q, want BYE", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(emitted, "") != "hello BYE" {
		t.Fatalf("emitted deltas = %q, want %q", emitted, []string{"hel", "lo ", "BYE"})
	}
	if !doneSeen {
		t.Fatal("no Done chunk was emitted")
	}
}

func TestRunStream_DegradesWithoutStreaming(t *testing.T) {
	// fakeProvider (delegate_test.go) has no Chat: must degrade to Run +
	// a single terminal emit.
	prov := &fakeProvider{text: "sub-done"}
	e := newTestEngine(prov)
	var emitted []string
	out, err := e.RunStream(context.Background(), "sys", "go", func(ck llm.Chunk) {
		emitted = append(emitted, ck.Delta)
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if out != "sub-done" {
		t.Fatalf("out = %q, want sub-done", out)
	}
	if len(emitted) != 1 || emitted[0] != "sub-done" {
		t.Fatalf("emitted = %q, want single [sub-done]", emitted)
	}
}
