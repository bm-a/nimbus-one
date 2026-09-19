package cron

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestJobsAddListRemove(t *testing.T) {
	r := NewRegistry()
	id, err := r.Add("0 9 * * *", "morning brief")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := r.Add("bogus", "x"); err == nil {
		t.Fatal("bad spec must error")
	}
	if _, err := r.Add("0 9 * * *", "  "); err == nil {
		t.Fatal("empty brief must error")
	}
	if len(r.List()) != 1 {
		t.Fatal("list must show the job")
	}
	if !r.Remove(id) || r.Remove(id) {
		t.Fatal("remove semantics wrong")
	}
}

func TestJobsDue(t *testing.T) {
	r := NewRegistry()
	id, err := r.Add("* * * * *", "every minute")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// Fresh job with zero LastRun: Due(zero, now) must fire (never ran).
	if len(r.Due(now)) != 1 {
		t.Fatal("fresh every-minute job must be due")
	}
	r.MarkRan(id, now)
	if len(r.Due(now.Add(time.Second))) != 0 {
		t.Fatal("just-ran job must not be due 1s later")
	}
}

func TestCronToolActions(t *testing.T) {
	tool := &CronTool{Jobs: NewRegistry()}
	ctx := context.Background()
	out, err := tool.Execute(ctx, map[string]any{"action": "schedule", "spec": "0 9 * * *", "brief": "b"})
	if err != nil || !strings.HasPrefix(out, "scheduled cron-") {
		t.Fatalf("schedule: %q %v", out, err)
	}
	out, err = tool.Execute(ctx, map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, "cron-1") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := tool.Execute(ctx, map[string]any{"action": "cancel", "id": "cron-1"}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := tool.Execute(ctx, map[string]any{"action": "frobnicate"}); err == nil {
		t.Fatal("unknown action must error")
	}
}
