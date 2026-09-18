package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"nimbus-one/internal/gateway"
)

func TestParseEvery(t *testing.T) {
	if got := ParseEvery("15m"); got != 15*time.Minute {
		t.Fatalf("ParseEvery(15m) = %v", got)
	}
	if got := ParseEvery("1h"); got != time.Hour {
		t.Fatalf("ParseEvery(1h) = %v", got)
	}
	if got := ParseEvery("bad"); got != 15*time.Minute {
		t.Fatalf("ParseEvery(bad) = %v, want 15m default", got)
	}
	if got := ParseEvery(""); got != 15*time.Minute {
		t.Fatalf("ParseEvery(empty) = %v, want 15m default", got)
	}
}

func TestSchedulerStartRunsTwiceThenStop(t *testing.T) {
	var n int64
	s := &Scheduler{Every: 20 * time.Millisecond, Fn: func(ctx context.Context) {
		atomic.AddInt64(&n, 1)
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Start(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&n) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Scheduler did not stop")
	}
	if atomic.LoadInt64(&n) < 2 {
		t.Fatalf("Fn ran %d times, want >= 2", n)
	}
}

type stubEngine struct {
	reply string
}

func (s *stubEngine) Run(ctx context.Context, system, user string) (string, error) {
	return s.reply, nil
}

func TestHeartbeatChecklist(t *testing.T) {
	dir := t.TempDir()
	content := "# Heartbeat\n\n- [ ] check inbox\n- [x] done item\n- [ ] review logs\nsome other line\n"
	if err := os.WriteFile(filepath.Join(dir, "HEARTBEAT.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Heartbeat{WorkspaceDir: dir}
	items := h.Checklist()
	if len(items) != 2 {
		t.Fatalf("Checklist = %v, want 2 unchecked items", items)
	}
	if items[0] != "check inbox" || items[1] != "review logs" {
		t.Fatalf("Checklist = %v", items)
	}
}

func TestHeartbeatTickNoReplySendsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "HEARTBEAT.md"), []byte("- [ ] check inbox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Broker with a counting engine: if Tick forwards anything, broker engine runs.
	var brokerCalls int64
	brokerEng := &countEngine{n: &brokerCalls}
	b := gateway.New(brokerEng, "")
	h := &Heartbeat{WorkspaceDir: dir, Broker: b, Eng: &stubEngine{reply: "NO_REPLY"}}
	results := h.Tick(context.Background())
	if len(results) != 0 {
		t.Fatalf("Tick results = %v, want none for NO_REPLY", results)
	}
	if atomic.LoadInt64(&brokerCalls) != 0 {
		t.Fatalf("broker engine calls = %d, want 0 (nothing forwarded)", brokerCalls)
	}
}

type countEngine struct {
	n *int64
}

func (c *countEngine) Run(ctx context.Context, system, user string) (string, error) {
	atomic.AddInt64(c.n, 1)
	return "ok", nil
}

func TestThrottleForBattery(t *testing.T) {
	base := time.Minute
	if got := ThrottleForBattery(5, base); got != 6*base {
		t.Fatalf("pct=5 -> %v, want 6x", got)
	}
	if got := ThrottleForBattery(10, base); got != 3*base {
		t.Fatalf("pct=10 -> %v, want 3x", got)
	}
	if got := ThrottleForBattery(15, base); got != 3*base {
		t.Fatalf("pct=15 -> %v, want 3x", got)
	}
	if got := ThrottleForBattery(80, base); got != base {
		t.Fatalf("pct=80 -> %v, want 1x", got)
	}
}

func TestIsTermuxBool(t *testing.T) {
	_ = IsTermux() // must not panic; returns bool
}
