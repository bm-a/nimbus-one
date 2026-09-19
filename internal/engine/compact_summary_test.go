package engine

import (
	"context"
	"strings"
	"testing"

	"nimbus-one/internal/llm"
)

func toolMsgs(n int, size int) []llm.Message {
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}}
	for i := 0; i < n; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleTool, Content: strings.Repeat("x", size), Name: "bash"})
	}
	return msgs
}

func TestPruneOldToolOutputs(t *testing.T) {
	msgs := toolMsgs(10, 5000)
	out := PruneOldToolOutputs(msgs, 6, 2000)
	// Input untouched.
	if len(msgs[1].Content) != 5000 {
		t.Fatal("input must not be mutated")
	}
	// Oldest 4 pruned (10+1 msgs, keep 6 → cutoff at index 5).
	for i := 1; i < 5; i++ {
		if !strings.Contains(out[i].Content, "[pruned:") {
			t.Fatalf("msg %d not pruned: %.60q", i, out[i].Content)
		}
	}
	// Newest kept intact.
	for i := 5; i < len(out); i++ {
		if len(out[i].Content) != 5000 {
			t.Fatalf("msg %d wrongly pruned", i)
		}
	}
}

func TestSummaryCompactHappy(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{{text: "SUMMARY: goal=file fix; files=a.go"}}}
	e := &Engine{LLM: prov, MaxSteps: 5}
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "turn"})
	}
	out := e.summaryCompact(context.Background(), msgs)
	if len(out) >= len(msgs) {
		t.Fatalf("not compacted: %d -> %d", len(msgs), len(out))
	}
	if out[0].Content != "sys" {
		t.Fatal("system prompt must survive first")
	}
	joined := ""
	for _, m := range out {
		joined += m.Content
	}
	if !strings.Contains(joined, "SUMMARY:") {
		t.Fatalf("summary missing: %q", joined)
	}
}

func TestSummaryCompactFallback(t *testing.T) {
	e := &Engine{LLM: nil, MaxSteps: 5} // summary fails -> extractive
	msgs := toolMsgs(20, 100)
	out := e.summaryCompact(context.Background(), msgs)
	if len(out) >= len(msgs) {
		t.Fatalf("fallback did not compact: %d -> %d", len(msgs), len(out))
	}
}

func TestMaybeCompactBudgetStages(t *testing.T) {
	prov := &reactScriptProvider{resps: []reactScriptResp{{text: "done"}}}
	e := &Engine{LLM: prov, MaxSteps: 3, BudgetWindow: 200}
	// ~50 tool msgs x ~500 tokens forces compact/over at window 200.
	msgs := toolMsgs(50, 2000)
	out := e.maybeCompact(context.Background(), msgs, nil)
	if len(out) >= len(msgs) {
		t.Fatalf("no compaction at over-budget: %d -> %d", len(msgs), len(out))
	}
}
