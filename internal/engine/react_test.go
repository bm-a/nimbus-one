package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/tools"
)

type reactScriptResp struct {
	text  string
	calls []llm.ToolCall
	err   error
}

type reactScriptProvider struct {
	resps    []reactScriptResp
	requests []llm.ChatRequest
}

func (p *reactScriptProvider) Name() string { return "react-fake" }

func (p *reactScriptProvider) Complete(_ context.Context, req llm.ChatRequest) (string, []llm.ToolCall, error) {
	p.requests = append(p.requests, req)
	i := len(p.requests) - 1
	if i >= len(p.resps) {
		i = len(p.resps) - 1
	}
	r := p.resps[i]
	return r.text, r.calls, r.err
}

func (p *reactScriptProvider) Chat(_ context.Context, _ llm.ChatRequest) (<-chan llm.Chunk, error) {
	return nil, fmt.Errorf("react-fake: Chat not implemented")
}

type reactUpperTool struct {
	executed bool
	gotText  string
}

func (t *reactUpperTool) Name() string { return "upper" }

func (t *reactUpperTool) Description() string { return "Uppercase text" }

func (t *reactUpperTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{
		"text": {Type: "string", Description: "text", Required: true},
	}
}

func (t *reactUpperTool) Execute(_ context.Context, args map[string]any) (string, error) {
	t.executed = true
	s, _ := args["text"].(string)
	t.gotText = s
	return strings.ToUpper(s), nil
}

func TestRun_ExecutesToolAndReturnsFinal(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "upper", Arguments: `{"text":"hi"}`}}},
		{text: "FINAL ANSWER"},
	}}
	ut := &reactUpperTool{}
	reg := tools.NewRegistry()
	reg.Register(ut)
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5}

	out, err := e.Run(context.Background(), "sys", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "FINAL ANSWER" {
		t.Fatalf("out = %q, want %q", out, "FINAL ANSWER")
	}
	if !ut.executed {
		t.Fatal("upper tool was not executed")
	}
	if ut.gotText != "hi" {
		t.Fatalf("tool got text = %q, want %q", ut.gotText, "hi")
	}
	if len(prov.requests) != 2 {
		t.Fatalf("llm calls = %d, want 2", len(prov.requests))
	}
	// Second request must carry the tool result.
	found := false
	for _, m := range prov.requests[1].Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "HI") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request messages = %+v, want tool result HI", prov.requests[1].Messages)
	}
}

func TestRun_UnknownToolFeedsBackError(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "nope", Arguments: `{}`}}},
		{text: "done after error"},
	}}
	reg := tools.NewRegistry() // empty: "nope" is unknown
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5}

	out, err := e.Run(context.Background(), "sys", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "done after error" {
		t.Fatalf("out = %q, want %q", out, "done after error")
	}
	if len(prov.requests) != 2 {
		t.Fatalf("llm calls = %d, want 2", len(prov.requests))
	}
	found := false
	for _, m := range prov.requests[1].Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "unknown tool") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request messages = %+v, want unknown-tool feedback", prov.requests[1].Messages)
	}
}
