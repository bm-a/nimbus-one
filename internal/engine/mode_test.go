package engine

import (
	"context"
	"strings"
	"testing"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/tools"
)

type modeStubTool struct {
	name     string
	out      string
	executed bool
}

func (t *modeStubTool) Name() string { return t.name }

func (t *modeStubTool) Description() string { return t.name + " tool" }

func (t *modeStubTool) Parameters() map[string]tools.Param {
	return map[string]tools.Param{}
}

func (t *modeStubTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	t.executed = true
	return t.out, nil
}

type modeCaptureProvider struct {
	resps    []reactScriptResp
	requests []llm.ChatRequest
}

func (p *modeCaptureProvider) Name() string { return "mode-fake" }

func (p *modeCaptureProvider) Complete(_ context.Context, req llm.ChatRequest) (string, []llm.ToolCall, error) {
	p.requests = append(p.requests, req)
	i := len(p.requests) - 1
	if i >= len(p.resps) {
		i = len(p.resps) - 1
	}
	r := p.resps[i]
	return r.text, r.calls, r.err
}

func (p *modeCaptureProvider) Chat(_ context.Context, _ llm.ChatRequest) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 1)
	ch <- llm.Chunk{Done: true}
	close(ch)
	return ch, nil
}

func modeTestRegistry() (*tools.Registry, *modeStubTool, *modeStubTool) {
	reg := tools.NewRegistry()
	rd := &modeStubTool{name: "read", out: "file contents"}
	ba := &modeStubTool{name: "bash", out: "shell output"}
	reg.Register(rd)
	reg.Register(ba)
	return reg, rd, ba
}

func toolDefNames(defs []llm.ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestPlanMode_HidesBashFromModel(t *testing.T) {
	reg, _, _ := modeTestRegistry()
	prov := &modeCaptureProvider{resps: []reactScriptResp{{text: "plan done"}}}
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 3, Mode: ModePlan}

	out, err := e.Run(context.Background(), "sys", "inspect")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "plan done" {
		t.Fatalf("out = %q", out)
	}
	if len(prov.requests) == 0 {
		t.Fatal("no requests captured")
	}
	names := toolDefNames(prov.requests[0].Tools)
	if !containsName(names, "read") {
		t.Fatalf("defs = %v, want read", names)
	}
	if containsName(names, "bash") {
		t.Fatalf("defs = %v, want bash hidden in plan mode", names)
	}
}

func TestPlanMode_BlocksMutatingTool(t *testing.T) {
	reg, _, bash := modeTestRegistry()
	prov := &modeCaptureProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"rm -rf /"}`}}},
		{text: "planned instead"},
	}}
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5, Mode: ModePlan}

	out, err := e.Run(context.Background(), "sys", "delete everything")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "planned instead" {
		t.Fatalf("out = %q", out)
	}
	if bash.executed {
		t.Fatal("bash must NOT execute in plan mode")
	}
	if len(prov.requests) != 2 {
		t.Fatalf("calls = %d, want 2", len(prov.requests))
	}
	found := false
	for _, m := range prov.requests[1].Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "plan mode") {
			found = true
		}
	}
	if !found {
		t.Fatalf("messages = %+v, want plan-mode block message", prov.requests[1].Messages)
	}
}

func TestBuildMode_AllowsBash(t *testing.T) {
	reg, _, bash := modeTestRegistry()
	prov := &modeCaptureProvider{resps: []reactScriptResp{
		{text: "", calls: []llm.ToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"echo hi"}`}}},
		{text: "built"},
	}}
	e := &Engine{LLM: prov, Tools: reg, MaxSteps: 5, Mode: ModeBuild}

	out, err := e.Run(context.Background(), "sys", "run cmd")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "built" {
		t.Fatalf("out = %q", out)
	}
	if !bash.executed {
		t.Fatal("bash must execute in build mode")
	}
	names := toolDefNames(prov.requests[0].Tools)
	if !containsName(names, "bash") {
		t.Fatalf("defs = %v, want bash in build mode", names)
	}
	found := false
	for _, m := range prov.requests[1].Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "shell output") {
			found = true
		}
	}
	if !found {
		t.Fatalf("messages = %+v, want real tool output", prov.requests[1].Messages)
	}
}
