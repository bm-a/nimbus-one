package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendTool_NotifyAccepted(t *testing.T) {
	var gotTarget, gotMsg string
	s := &SendTool{Send: func(target, msg string) (string, bool, error) {
		gotTarget, gotMsg = target, msg
		return "", false, nil
	}}
	out, err := s.Execute(context.Background(), map[string]any{
		"target_session": "sess-1", "message": "hello", "mode": "notify",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "accepted") {
		t.Fatalf("out = %q, want accepted", out)
	}
	if gotTarget != "sess-1" || gotMsg != "hello" {
		t.Fatalf("Send got (%q,%q), want (sess-1,hello)", gotTarget, gotMsg)
	}
}

func TestSendTool_SteerWaitsForReply(t *testing.T) {
	s := &SendTool{Send: func(target, msg string) (string, bool, error) {
		return "hello-back", false, nil
	}}
	out, err := s.Execute(context.Background(), map[string]any{
		"target_session": "sess-1", "message": "hi", "mode": "steer", "timeout_s": 5.0,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "hello-back" {
		t.Fatalf("out = %q, want hello-back", out)
	}
}

func TestSendTool_SteerPendingReply(t *testing.T) {
	s := &SendTool{Send: func(target, msg string) (string, bool, error) {
		return "partial…", true, nil
	}}
	out, err := s.Execute(context.Background(), map[string]any{
		"target_session": "sess-1", "message": "hi", "mode": "followup",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "pending") {
		t.Fatalf("out = %q, want pending", out)
	}
}

func TestSendTool_SteerTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := &SendTool{Send: func(target, msg string) (string, bool, error) {
		<-release // blocks until test ends
		return "late", false, nil
	}}
	out, err := s.Execute(context.Background(), map[string]any{
		"target_session": "sess-1", "message": "hi", "mode": "steer", "timeout_s": 0.05,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("out = %q, want timeout status", out)
	}
}

func TestSendTool_Errors(t *testing.T) {
	s := &SendTool{Send: func(t, m string) (string, bool, error) { return "", false, nil }}
	for name, args := range map[string]map[string]any{
		"missing target":  {"message": "hi"},
		"missing message": {"target_session": "s"},
		"unknown mode":    {"target_session": "s", "message": "hi", "mode": "teleport"},
	} {
		if _, err := s.Execute(context.Background(), args); err == nil {
			t.Fatalf("%s: got nil error, want error", name)
		}
	}
	if _, err := (&SendTool{}).Execute(context.Background(), map[string]any{"target_session": "s", "message": "m"}); err == nil {
		t.Fatal("nil Send: got nil error, want error")
	}
}

func TestYieldTool_ClaimAndAwait(t *testing.T) {
	var claims int32
	y := &YieldTool{
		Claim: func() bool { return atomic.AddInt32(&claims, 1) == 1 },
		Await: func(ctx context.Context) (string, error) { return "child-done", nil },
	}
	out, err := y.Execute(context.Background(), map[string]any{"reason": "waiting on child"})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if out != "child-done" {
		t.Fatalf("out = %q, want child-done", out)
	}
	out, err = y.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if !strings.Contains(out, "already claimed") {
		t.Fatalf("out = %q, want already-claimed", out)
	}
	if _, err := (&YieldTool{}).Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("unwired: got nil error, want error")
	}
}

func TestWaitTool_AllDone(t *testing.T) {
	ts := NewTasks()
	w := &WaitTool{Tasks: ts}
	ctx := context.Background()
	id1 := ts.Start("a", func(ctx context.Context) (string, error) { return "r1", nil })
	id2 := ts.Start("b", func(ctx context.Context) (string, error) { return "r2", nil })
	out, err := w.Execute(ctx, map[string]any{"ids": []any{id1, id2}, "timeout_s": 5.0})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, id1+": done") || !strings.Contains(out, id2+": done") {
		t.Fatalf("out = %q, want both done", out)
	}
}

func TestWaitTool_Timeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	ts := NewTasks()
	w := &WaitTool{Tasks: ts}
	id := ts.Start("slow", func(ctx context.Context) (string, error) {
		select {
		case <-release:
			return "x", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	out, err := w.Execute(context.Background(), map[string]any{"ids": id, "timeout_s": 0.05})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "timeout") || !strings.Contains(out, id) {
		t.Fatalf("out = %q, want timeout mentioning %s", out, id)
	}
	if _, err := w.Execute(context.Background(), map[string]any{"ids": []any{"task-999"}}); err == nil {
		t.Fatal("unknown id: got nil error, want error")
	}
}

func TestStructuredTool(t *testing.T) {
	var got string
	s := &StructuredTool{Schema: `{"type":"object"}`, Result: &got}
	out, err := s.Execute(context.Background(), map[string]any{"result": `{"a":1}`})
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if !strings.Contains(out, "recorded") || got != `{"a":1}` {
		t.Fatalf("out = %q, stored = %q", out, got)
	}
	if _, err := s.Execute(context.Background(), map[string]any{"result": "not-json"}); err == nil {
		t.Fatal("invalid JSON with schema: got nil error, want error")
	}
	if _, err := s.Execute(context.Background(), map[string]any{"result": "   "}); err == nil {
		t.Fatal("empty: got nil error, want error")
	}
	plain := &StructuredTool{}
	if _, err := plain.Execute(context.Background(), map[string]any{"result": "free text ok"}); err != nil {
		t.Fatalf("no-schema text: %v", err)
	}
	_ = time.Now
}
