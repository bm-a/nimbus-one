package engine

import (
	"strings"
	"testing"

	"nimbus-one/internal/llm"
)

func TestShouldCompact_Threshold80Percent(t *testing.T) {
	c := &Compactor{Limit: 100}
	if !c.ShouldCompact(80) {
		t.Fatal("ShouldCompact(80) = false, want true at 80% of 100")
	}
	if !c.ShouldCompact(100) {
		t.Fatal("ShouldCompact(100) = false, want true")
	}
	if c.ShouldCompact(79) {
		t.Fatal("ShouldCompact(79) = true, want false below 80%")
	}
	if c.ShouldCompact(0) {
		t.Fatal("ShouldCompact(0) = true, want false")
	}
	var nilC *Compactor
	if nilC.ShouldCompact(1000000) {
		t.Fatal("nil ShouldCompact = true, want false")
	}
	zero := &Compactor{}
	if zero.ShouldCompact(1000000) {
		t.Fatal("zero-limit ShouldCompact = true, want false")
	}
}

func TestCompact_PreservesSystemAndLastN(t *testing.T) {
	c := &Compactor{Limit: 1000}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "m1"},
		{Role: llm.RoleAssistant, Content: "m2"},
		{Role: llm.RoleUser, Content: "m3"},
		{Role: llm.RoleAssistant, Content: "m4"},
		{Role: llm.RoleUser, Content: "m5"},
	}
	out := c.Compact(msgs, 2)
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4 (system + summary + last 2)", len(out))
	}
	if out[0].Role != llm.RoleSystem || out[0].Content != "system prompt" {
		t.Fatalf("out[0] = %+v, want original system", out[0])
	}
	if !strings.Contains(strings.ToLower(out[1].Content), "summar") {
		t.Fatalf("out[1] = %q, want summary", out[1].Content)
	}
	if out[2].Content != "m4" || out[3].Content != "m5" {
		t.Fatalf("tail = %+v, want last 2 preserved", out[2:])
	}
}

func TestCompact_ShortHistoryUnchanged(t *testing.T) {
	c := &Compactor{Limit: 1000}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
	}
	out := c.Compact(msgs, 5)
	if len(out) != len(msgs) {
		t.Fatalf("len = %d, want %d", len(out), len(msgs))
	}
}

func TestBuildPrompt_ContainsSections(t *testing.T) {
	p := BuildPrompt("soul-text", "user-ctx", "mem-ctx", []string{"fact one"}, "skill-cap")
	for _, want := range []string{"soul-text", "user-ctx", "mem-ctx", "fact one", "skill-cap"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	if !strings.Contains(p, "Memory:") {
		t.Fatalf("prompt missing Memory section:\n%s", p)
	}
	if !strings.Contains(p, "Capabilities") {
		t.Fatalf("prompt missing skills/capabilities section:\n%s", p)
	}
	if !strings.Contains(p, "Known facts:") {
		t.Fatalf("prompt missing facts section:\n%s", p)
	}
	if BuildPrompt("", "", "", nil, "") != "" {
		t.Fatal("empty BuildPrompt want empty string")
	}
}
