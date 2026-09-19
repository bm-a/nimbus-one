package hooks

import (
	"strings"
	"testing"
)

func TestTrigger_Order(t *testing.T) {
	r := NewRegistry()
	r.On("session:reset", func(Event) string { return "first" })
	r.On("session:reset", func(Event) string { return "second" })
	r.On("session:reset", func(Event) string { return "" }) // empty: no message
	got := r.Trigger(Event{Key: "session:reset", Family: "session", Action: "reset"})
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("got %v, want [first second] in registration order", got)
	}
}

func TestTrigger_RecoverContinuesFanOut(t *testing.T) {
	r := NewRegistry()
	ran := false
	r.On("build", func(Event) string { panic("boom") })
	r.On("build", func(Event) string { ran = true; return "after-panic" })
	got := r.Trigger(Event{Key: "build", Family: "build", Action: "done"})
	if !ran {
		t.Fatal("handler after a panicking one did not run (fan-out short-circuited)")
	}
	if len(got) != 2 || !strings.Contains(got[0], "boom") || got[1] != "after-panic" {
		t.Fatalf("got %v, want [panic-msg after-panic]", got)
	}
}

func TestTrigger_Disabled(t *testing.T) {
	r := NewRegistry()
	ran := false
	r.On("x", func(Event) string { ran = true; return "m" })
	r.SetEnabled(false)
	if got := r.Trigger(Event{Key: "x"}); got != nil {
		t.Fatalf("disabled Trigger = %v, want nil", got)
	}
	if ran {
		t.Fatal("disabled registry still invoked a handler")
	}
	r.SetEnabled(true)
	if got := r.Trigger(Event{Key: "x"}); len(got) != 1 {
		t.Fatalf("re-enabled Trigger = %v, want [m]", got)
	}
}

func TestTrigger_FamilyPrefix(t *testing.T) {
	r := NewRegistry()
	r.On("session", func(ev Event) string { return "family:" + ev.Key })
	r.On("other", func(Event) string { return "unrelated" })
	got := r.Trigger(Event{Key: "session:auto-reset", Family: "session", Action: "auto-reset"})
	if len(got) != 1 || got[0] != "family:session:auto-reset" {
		t.Fatalf("got %v, want family handler to match session:auto-reset", got)
	}
	// Exact family match also fires.
	got = r.Trigger(Event{Key: "session", Family: "session", Action: "reset"})
	if len(got) != 1 {
		t.Fatalf("exact match got %v, want one message", got)
	}
	// Non-matching prefix must not fire: "sess" is not a family of "session:x".
	r2 := NewRegistry()
	r2.On("sess", func(Event) string { return "nope" })
	if got := r2.Trigger(Event{Key: "session:x", Family: "session"}); len(got) != 0 {
		t.Fatalf("partial-prefix match fired: %v", got)
	}
}

func TestTrigger_EventDataPassthrough(t *testing.T) {
	r := NewRegistry()
	var seen map[string]any
	r.On("deploy", func(ev Event) string {
		seen = ev.Data
		return ev.Action
	})
	got := r.Trigger(Event{Key: "deploy:done", Family: "deploy", Action: "done", Data: map[string]any{"n": 1}})
	if len(got) != 1 || got[0] != "done" {
		t.Fatalf("got %v, want [done]", got)
	}
	if seen["n"] != 1 {
		t.Fatalf("data = %v, want n=1", seen)
	}
}
