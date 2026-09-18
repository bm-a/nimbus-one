package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitForState(t *testing.T, ts *Tasks, id string, want string, timeout time.Duration) Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got, ok := ts.Get(id)
		if !ok {
			t.Fatalf("task %q disappeared while waiting for %q", id, want)
		}
		if got.State == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for task %q state %q (last=%q)", id, want, got.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTasks_StartGetDone(t *testing.T) {
	ts := NewTasks()
	id := ts.Start("hello", func(ctx context.Context) (string, error) {
		return "done-result", nil
	})
	got := waitForState(t, ts, id, TaskDone, 2*time.Second)
	if got.Result != "done-result" {
		t.Fatalf("Result = %q, want %q", got.Result, "done-result")
	}
	if got.Input != "hello" {
		t.Fatalf("Input = %q, want %q", got.Input, "hello")
	}
	if got.ID != id {
		t.Fatalf("ID = %q, want %q", got.ID, id)
	}
}

func TestTasks_GetMissing(t *testing.T) {
	ts := NewTasks()
	if _, ok := ts.Get("task-999"); ok {
		t.Fatal("Get(unknown) ok=true, want false")
	}
}

func TestTasks_CancelRunning(t *testing.T) {
	ts := NewTasks()
	release := make(chan struct{})
	id := ts.Start("blocking", func(ctx context.Context) (string, error) {
		select {
		case <-release:
			return "should-not-happen", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	// Give the goroutine a moment to start and block.
	time.Sleep(20 * time.Millisecond)
	if !ts.Cancel(id) {
		close(release)
		t.Fatal("Cancel(running) = false, want true")
	}
	got, ok := ts.Get(id)
	if !ok {
		close(release)
		t.Fatal("Get after Cancel: missing task")
	}
	if got.State != TaskCancelled {
		close(release)
		t.Fatalf("State = %q, want %q", got.State, TaskCancelled)
	}
	close(release)
	// Let the background goroutine observe cancellation and settle.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ = ts.Get(id)
		if got.State == TaskCancelled && got.Err == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-cancel settle: State=%q Err=%q, want cancelled/cancelled", got.State, got.Err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTasks_CancelUnknown(t *testing.T) {
	ts := NewTasks()
	if ts.Cancel("task-999") {
		t.Fatal("Cancel(unknown) = true, want false")
	}
}

func TestTasks_CancelFinishedFalse(t *testing.T) {
	ts := NewTasks()
	id := ts.Start("quick", func(ctx context.Context) (string, error) { return "ok", nil })
	waitForState(t, ts, id, TaskDone, 2*time.Second)
	if ts.Cancel(id) {
		t.Fatal("Cancel(finished) = true, want false")
	}
}

func TestTasks_ListNewestFirst(t *testing.T) {
	ts := NewTasks()
	first := ts.Start("first", func(ctx context.Context) (string, error) { return "1", nil })
	time.Sleep(15 * time.Millisecond)
	second := ts.Start("second", func(ctx context.Context) (string, error) { return "2", nil })
	waitForState(t, ts, first, TaskDone, 2*time.Second)
	waitForState(t, ts, second, TaskDone, 2*time.Second)

	all := ts.List()
	if len(all) != 2 {
		t.Fatalf("List len = %d, want 2", len(all))
	}
	if all[0].ID != second || all[1].ID != first {
		t.Fatalf("List order = [%s %s], want [%s %s] (newest first)", all[0].ID, all[1].ID, second, first)
	}
}

func TestTasks_PruneDropsOldFinishedKeepsRunning(t *testing.T) {
	ts := NewTasks()
	release := make(chan struct{})
	defer close(release)
	running := ts.Start("running", func(ctx context.Context) (string, error) {
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	doneID := ts.Start("finished", func(ctx context.Context) (string, error) { return "ok", nil })
	waitForState(t, ts, doneID, TaskDone, 2*time.Second)

	// Age the finished task so Prune deterministically drops it.
	ts.mu.Lock()
	if task, ok := ts.m[doneID]; ok {
		task.DoneAt = time.Now().Add(-time.Hour)
	}
	ts.mu.Unlock()

	dropped := ts.Prune(time.Minute)
	if dropped != 1 {
		t.Fatalf("Prune dropped = %d, want 1", dropped)
	}
	if _, ok := ts.Get(doneID); ok {
		t.Fatalf("finished task %q survived Prune, want dropped", doneID)
	}
	if _, ok := ts.Get(running); !ok {
		t.Fatalf("running task %q dropped by Prune, want kept", running)
	}
	// A failed task aged the same way is also pruned.
	failID := ts.Start("fail", func(ctx context.Context) (string, error) { return "", errors.New("boom") })
	waitForState(t, ts, failID, TaskFailed, 2*time.Second)
	ts.mu.Lock()
	if task, ok := ts.m[failID]; ok {
		task.DoneAt = time.Now().Add(-time.Hour)
	}
	ts.mu.Unlock()
	if n := ts.Prune(time.Minute); n != 1 {
		t.Fatalf("Prune failed-task dropped = %d, want 1", n)
	}
}
