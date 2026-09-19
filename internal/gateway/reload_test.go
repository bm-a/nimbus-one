// Tests for reload.go.
package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bump mtime/size deterministically: FS timestamp granularity varies, so
// force the mtime forward instead of sleeping.
func watcherTouch(t *testing.T, path string, body string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherPollDetectsChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	watcherTouch(t, path, "v1", base)

	var calls int
	w := NewWatcher(path, time.Second, func() { calls++ })
	if w.Poll() {
		t.Fatal("first Poll establishes the baseline, must not fire")
	}
	if w.Poll() {
		t.Fatal("unchanged file must not fire")
	}
	watcherTouch(t, path, "v1-changed", base.Add(2*time.Second))
	if !w.Poll() {
		t.Fatal("modification must fire")
	}
	if calls != 1 {
		t.Fatalf("OnChange calls = %d, want 1", calls)
	}
	if w.Poll() {
		t.Fatal("steady state after a change must not re-fire")
	}
}

func TestWatcherPollAppearanceAndDisappearance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	var calls int
	w := NewWatcher(path, time.Second, func() { calls++ })
	if w.Poll() {
		t.Fatal("missing file baseline must not fire")
	}
	watcherTouch(t, path, "v1", time.Now().Add(-time.Hour))
	if !w.Poll() {
		t.Fatal("appearance must fire")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !w.Poll() {
		t.Fatal("disappearance must fire")
	}
	if calls != 2 {
		t.Fatalf("OnChange calls = %d, want 2", calls)
	}
}

func TestWatcherPollNilCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	watcherTouch(t, path, "v1", time.Now().Add(-time.Hour))
	w := NewWatcher(path, time.Second, nil)
	if w.Poll() {
		t.Fatal("baseline must not fire")
	}
	watcherTouch(t, path, "v2", time.Now())
	if !w.Poll() {
		t.Fatal("change must be reported even with nil OnChange")
	}
}

func TestWatcherCallbackPanicRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	watcherTouch(t, path, "v1", time.Now().Add(-time.Hour))
	w := NewWatcher(path, time.Second, func() { panic("bad callback") })
	_ = w.Poll()
	watcherTouch(t, path, "v2", time.Now())
	if !w.Poll() {
		t.Fatal("panicking callback must still count as a fired change, not crash")
	}
}

func TestWatcherRunFires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	watcherTouch(t, path, "v1", time.Now().Add(-time.Hour).Truncate(time.Second))

	fired := make(chan struct{}, 4)
	w := NewWatcher(path, 10*time.Millisecond, func() { fired <- struct{}{} })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go w.Run(ctx)
	_ = w.Poll() // establish baseline outside the loop race window
	watcherTouch(t, path, "v2", time.Now().Add(time.Hour))
	select {
	case <-fired:
	case <-ctx.Done():
		t.Fatal("Run did not fire OnChange after modification")
	}
}

func TestWatcherNilSafe(t *testing.T) {
	var w *Watcher
	if w.Poll() {
		t.Fatal("nil watcher Poll must be false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w.Run(ctx) // must return, not hang or panic
}
