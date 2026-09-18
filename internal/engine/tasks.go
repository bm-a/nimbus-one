package engine

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Task states for background work.
const (
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

// Task is one background unit of work.
type Task struct {
	ID        string
	Input     string
	Result    string
	Err       string
	State     string
	CreatedAt time.Time
	DoneAt    time.Time

	cancel context.CancelFunc
}

// Tasks runs agent work in the background: start returns an id immediately,
// poll retrieves the outcome, cancel stops it. Safe for serve/daemon use.
type Tasks struct {
	mu  sync.Mutex
	seq int64
	m   map[string]*Task
}

// NewTasks creates an empty registry.
func NewTasks() *Tasks { return &Tasks{m: map[string]*Task{}} }

// Start launches fn in the background and returns its id.
func (t *Tasks) Start(input string, fn func(ctx context.Context) (string, error)) string {
	t.mu.Lock()
	t.seq++
	id := fmt.Sprintf("task-%d", t.seq)
	ctx, cancel := context.WithCancel(context.Background())
	t.m[id] = &Task{ID: id, Input: input, State: TaskRunning, CreatedAt: time.Now(), cancel: cancel}
	t.mu.Unlock()
	go func() {
		res, err := fn(ctx)
		t.mu.Lock()
		defer t.mu.Unlock()
		task, ok := t.m[id]
		if !ok {
			return
		}
		task.DoneAt = time.Now()
		if ctx.Err() == context.Canceled && err != nil {
			task.State = TaskCancelled
			task.Err = "cancelled"
			return
		}
		if err != nil {
			task.State = TaskFailed
			task.Err = err.Error()
			return
		}
		task.State = TaskDone
		task.Result = res
	}()
	return id
}

// Get returns a copy of a task.
func (t *Tasks) Get(id string) (Task, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	task, ok := t.m[id]
	if !ok {
		return Task{}, false
	}
	cp := *task
	cp.cancel = nil
	return cp, true
}

// Cancel stops a running task. False when unknown or already finished.
func (t *Tasks) Cancel(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	task, ok := t.m[id]
	if !ok || task.State != TaskRunning {
		return false
	}
	if task.cancel != nil {
		task.cancel()
	}
	task.State = TaskCancelled
	task.DoneAt = time.Now()
	return true
}

// List returns copies of all tasks, newest first.
func (t *Tasks) List() []Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Task, 0, len(t.m))
	for _, task := range t.m {
		cp := *task
		cp.cancel = nil
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Prune drops finished tasks older than maxAge. Returns the dropped count.
func (t *Tasks) Prune(maxAge time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	n := 0
	for id, task := range t.m {
		if task.State != TaskRunning && now.Sub(task.DoneAt) > maxAge {
			delete(t.m, id)
			n++
		}
	}
	return n
}
