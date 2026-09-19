package engine

import (
	"testing"

	"nimbus-one/internal/llm"
)

func TestChildSpec_ForkDifferentAgentFallsBack(t *testing.T) {
	spec, note := ChildSpec{Mode: "fork"}.Resolve("isolated", false)
	if spec.Mode != ChildIsolated {
		t.Fatalf("mode = %q, want isolated", spec.Mode)
	}
	if note == "" {
		t.Fatal("note is empty, want fallback explanation")
	}
}

func TestChildSpec_ForkSameAgentKept(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	spec, note := ChildSpec{Mode: "fork", Transcript: msgs}.Resolve("isolated", true)
	if spec.Mode != ChildFork {
		t.Fatalf("mode = %q, want fork", spec.Mode)
	}
	if note != "" {
		t.Fatalf("note = %q, want empty", note)
	}
	if len(spec.Transcript) != 1 {
		t.Fatal("fork must keep the transcript")
	}
}

func TestChildSpec_LightDropsTranscript(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	spec, _ := ChildSpec{Mode: "light", Transcript: msgs}.Resolve("isolated", true)
	if spec.Mode != ChildLight {
		t.Fatalf("mode = %q, want light", spec.Mode)
	}
	if len(spec.Transcript) != 0 {
		t.Fatalf("light kept %d transcript messages, want 0", len(spec.Transcript))
	}
}

func TestChildSpec_DefaultsAndUnknown(t *testing.T) {
	spec, _ := ChildSpec{}.Resolve("", true)
	if spec.Mode != ChildIsolated {
		t.Fatalf("empty mode = %q, want isolated", spec.Mode)
	}
	spec, note := ChildSpec{Mode: "teleport"}.Resolve("", true)
	if spec.Mode != ChildIsolated || note == "" {
		t.Fatalf("unknown mode = (%q,%q), want isolated + note", spec.Mode, note)
	}
}

func TestForkTranscript(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "1"},
		{Role: llm.RoleUser, Content: "2"},
		{Role: llm.RoleUser, Content: "3"},
		{Role: llm.RoleUser, Content: "4"},
		{Role: llm.RoleUser, Content: "5"},
	}
	got := ForkTranscript(msgs, 2)
	if len(got) != 2 || got[0].Content != "4" || got[1].Content != "5" {
		t.Fatalf("keepLast=2 gave %+v, want last two", got)
	}
	if full := ForkTranscript(msgs, 0); len(full) != 5 {
		t.Fatalf("keepLast=0 gave %d, want full copy of 5", len(full))
	}
	if full := ForkTranscript(msgs, 99); len(full) != 5 {
		t.Fatalf("keepLast=99 gave %d, want 5", len(full))
	}
	// The copy must not alias the input.
	got[0].Content = "mutated"
	if msgs[3].Content != "4" {
		t.Fatal("ForkTranscript aliases the input slice")
	}
	if ForkTranscript(nil, 2) != nil {
		t.Fatal("nil input: want nil")
	}
}
