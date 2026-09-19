package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/tools"
)

// fakeProvider scripts Complete results and counts calls (goroutine-safe).
type fakeProvider struct {
	mu    sync.Mutex
	calls int
	text  string
	err   error
}

func (f *fakeProvider) Name() string { return "delegate-fake" }

func (f *fakeProvider) Complete(_ context.Context, _ llm.ChatRequest) (string, []llm.ToolCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.text, nil, f.err
}

func (f *fakeProvider) Chat(_ context.Context, _ llm.ChatRequest) (<-chan llm.Chunk, error) {
	return nil, fmt.Errorf("delegate-fake: Chat not implemented")
}

func (f *fakeProvider) numCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestEngine(prov llm.Provider) *Engine {
	return &Engine{LLM: prov, Tools: tools.NewRegistry(), MaxSteps: 5}
}

// extractTaskID pulls the first "task-N" token out of s.
func extractTaskID(s string) string {
	idx := strings.Index(s, "task-")
	if idx < 0 {
		return ""
	}
	j := idx + len("task-")
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[idx:j]
}

func TestDelegate_SyncReturnsResult(t *testing.T) {
	prov := &fakeProvider{text: "sub-done"}
	eng := newTestEngine(prov)
	d := &DelegateTool{Eng: eng, Tasks: NewTasks()}

	out, err := d.Execute(context.Background(), map[string]any{"brief": "do the thing"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "sub-done" {
		t.Fatalf("Execute = %q, want %q", out, "sub-done")
	}
	if n := prov.numCalls(); n != 1 {
		t.Fatalf("provider calls = %d, want 1", n)
	}
}

func TestDelegate_DepthCapRefuses(t *testing.T) {
	prov := &fakeProvider{text: "sub-done"}
	eng := newTestEngine(prov)
	d := &DelegateTool{Eng: eng, Tasks: NewTasks()}

	ctx := withDepth(withDepth(context.Background()))
	if depthOf(ctx) != 2 {
		t.Fatalf("depthOf = %d, want 2", depthOf(ctx))
	}
	out, err := d.Execute(ctx, map[string]any{"brief": "nested work"})
	if err != nil {
		t.Fatalf("Execute at depth cap: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "depth cap") {
		t.Fatalf("refusal = %q, want it to mention depth cap", out)
	}
	if n := prov.numCalls(); n != 0 {
		t.Fatalf("provider calls = %d, want 0 (must not be called)", n)
	}
}

func TestDelegate_AsyncPollDone(t *testing.T) {
	prov := &fakeProvider{text: "sub-done"}
	eng := newTestEngine(prov)
	shared := NewTasks()
	d := &DelegateTool{Eng: eng, Tasks: shared}
	pt := &TasksTool{Tasks: shared}

	out, err := d.Execute(context.Background(), map[string]any{"brief": "long work", "background": true})
	if err != nil {
		t.Fatalf("async Execute: %v", err)
	}
	id := extractTaskID(out)
	if id == "" {
		t.Fatalf("async Execute returned %q, want it to contain a task id", out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := pt.Execute(context.Background(), map[string]any{"action": "poll", "id": id})
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if strings.Contains(got, "sub-done") && strings.Contains(got, "done") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for async result; last poll = %q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTasksTool_ListCancelUnknownAction(t *testing.T) {
	ts := NewTasks()
	pt := &TasksTool{Tasks: ts}
	ctx := context.Background()

	// Empty list.
	out, err := pt.Execute(ctx, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if out != "no background tasks" {
		t.Fatalf("list empty = %q, want %q", out, "no background tasks")
	}

	// List with a finished task.
	id := ts.Start("brief-one", func(ctx context.Context) (string, error) { return "r", nil })
	deadline := time.Now().Add(2 * time.Second)
	for {
		task, ok := ts.Get(id)
		if ok && task.State == TaskDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for task to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	out, err = pt.Execute(ctx, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, id) || !strings.Contains(out, TaskDone) {
		t.Fatalf("list = %q, want it to contain %q and %q", out, id, TaskDone)
	}

	// Poll done includes the result.
	out, err = pt.Execute(ctx, map[string]any{"action": "poll", "id": id})
	if err != nil {
		t.Fatalf("poll done: %v", err)
	}
	if !strings.Contains(out, "done") || !strings.Contains(out, "r") {
		t.Fatalf("poll done = %q, want done + result", out)
	}

	// Cancel a finished task errors.
	if _, err = pt.Execute(ctx, map[string]any{"action": "cancel", "id": id}); err == nil {
		t.Fatal("cancel finished: got nil error, want error")
	}

	// Cancel a running task succeeds.
	release := make(chan struct{})
	defer close(release)
	running := ts.Start("brief-two", func(ctx context.Context) (string, error) {
		select {
		case <-release:
			return "x", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	time.Sleep(20 * time.Millisecond)
	out, err = pt.Execute(ctx, map[string]any{"action": "cancel", "id": running})
	if err != nil {
		t.Fatalf("cancel running: %v", err)
	}
	if !strings.Contains(out, "cancelled") {
		t.Fatalf("cancel running = %q, want cancelled", out)
	}

	// Cancel unknown errors.
	if _, err = pt.Execute(ctx, map[string]any{"action": "cancel", "id": "task-999"}); err == nil {
		t.Fatal("cancel unknown: got nil error, want error")
	}

	// Poll unknown errors.
	if _, err = pt.Execute(ctx, map[string]any{"action": "poll", "id": "task-999"}); err == nil {
		t.Fatal("poll unknown: got nil error, want error")
	}

	// Unknown action errors.
	if _, err = pt.Execute(ctx, map[string]any{"action": "explode"}); err == nil {
		t.Fatal("unknown action: got nil error, want error")
	} else if !strings.Contains(strings.ToLower(err.Error()), "unknown action") {
		t.Fatalf("unknown action err = %q, want it to mention unknown action", err)
	}
}

func TestDelegate_NilEngineErrors(t *testing.T) {
	d := &DelegateTool{Eng: nil, Tasks: NewTasks()}
	if _, err := d.Execute(context.Background(), map[string]any{"brief": "work"}); err == nil {
		t.Fatal("nil Eng: got nil error, want error")
	}
}

func TestDelegate_EmptyBriefErrors(t *testing.T) {
	prov := &fakeProvider{text: "sub-done"}
	d := &DelegateTool{Eng: newTestEngine(prov), Tasks: NewTasks()}
	for _, args := range []map[string]any{{}, {"brief": ""}, {"brief": "   "}} {
		if _, err := d.Execute(context.Background(), args); err == nil {
			t.Fatalf("args %v: got nil error, want error", args)
		}
	}
	if n := prov.numCalls(); n != 0 {
		t.Fatalf("provider calls = %d, want 0 for empty brief", n)
	}
}

func TestDelegate_UnknownAgentErrors(t *testing.T) {
	prov := &fakeProvider{text: "x"}
	d := &DelegateTool{Eng: newTestEngine(prov), Tasks: NewTasks()}
	if _, err := d.Execute(context.Background(), map[string]any{"brief": "b", "agent": "frobnicate"}); err == nil {
		t.Fatal("unknown agent kind must error")
	}
}

func TestDelegate_ExploreChildIsReadOnly(t *testing.T) {
	// Parent allows everything; explore child must still hide bash.
	prov := &fakeProvider{text: "child says hi"}
	parent := newTestEngine(prov)
	parent.Ruleset = nil // build default
	d := &DelegateTool{Eng: parent, Tasks: NewTasks()}
	out, err := d.Execute(context.Background(), map[string]any{"brief": "look around", "agent": "explore"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "child says hi" {
		t.Fatalf("out = %q", out)
	}
	// Enforcement proof: a mutating call inside an explore child is
	// blocked even though the parent allows everything.
	calls := []llm.ToolCall{{ID: "1", Name: "bash", Arguments: `{"command":"rm -rf /"}`}}
	execProv := &reactScriptProvider{resps: []reactScriptResp{
		{text: "", calls: calls},
		{text: "after block"},
	}}
	execParent := &Engine{LLM: execProv, Tools: tools.NewRegistry(), MaxSteps: 5}
	d2 := &DelegateTool{Eng: execParent, Tasks: NewTasks()}
	if _, err := d2.Execute(context.Background(), map[string]any{"brief": "b", "agent": "explore"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	blocked := false
	for _, req := range execProv.requests {
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, "plan mode is read-only") {
				blocked = true
			}
		}
	}
	if !blocked {
		t.Fatal("explore child did not block the mutating call")
	}
}

func TestDelegate_ModelRouting(t *testing.T) {
	parent := newTestEngine(&fakeProvider{text: "done"})
	parent.ModelID = "main-model"
	d := &DelegateTool{Eng: parent, Tasks: NewTasks(), Models: map[string]string{"explore": "fast-model"}}
	if got := llm.RouteModel("explore", "", d.Models, d.Eng.ModelID); got != "fast-model" {
		t.Fatalf("kind route = %q, want fast-model", got)
	}
	if got := llm.RouteModel("general", "per-call", d.Models, d.Eng.ModelID); got != "per-call" {
		t.Fatalf("explicit = %q", got)
	}
	if got := llm.RouteModel("general", "", d.Models, d.Eng.ModelID); got != "main-model" {
		t.Fatalf("inherit = %q", got)
	}
}
