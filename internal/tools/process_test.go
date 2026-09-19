package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessStartPollDone(t *testing.T) {
	m := NewProcessManager()
	id, err := m.Start("echo hello-proc", "", 30*time.Second)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatal("empty session id")
	}
	deadline := time.Now().Add(10 * time.Second)
	var out string
	for {
		out, err = m.Poll(id)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if strings.Contains(out, "done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never finished: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out, "hello-proc") {
		t.Fatalf("tail must contain output: %q", out)
	}
	if got := m.Prune(); got != 1 {
		t.Fatalf("Prune = %d, want 1", got)
	}
	if _, err := m.Poll(id); err == nil {
		t.Fatal("poll after prune must error")
	}
}

func TestProcessLogOffsetPaging(t *testing.T) {
	m := NewProcessManager()
	id, err := m.Start("printf 'aaa\\nbbb\\nccc\\n'", "", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := m.Poll(id)
		if strings.Contains(out, "done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}
	page1, next, err := m.Log(id, 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !strings.Contains(page1, "aaa") || next <= 0 {
		t.Fatalf("page1=%q next=%d", page1, next)
	}
	rest, next2, err := m.Log(id, next)
	if err != nil {
		t.Fatalf("Log page2: %v", err)
	}
	if rest != "" || next2 != next {
		t.Fatalf("page2=%q next2=%d, want empty + same cursor", rest, next2)
	}
}

func TestProcessWriteAndKill(t *testing.T) {
	m := NewProcessManager()
	id, err := m.Start("cat", "", 60*time.Second)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Write(id, "ping-stdin"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := m.Poll(id)
		if strings.Contains(out, "ping-stdin") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdin echo never appeared: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	msg, err := m.Kill(id)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !strings.Contains(msg, id) {
		t.Fatalf("kill msg = %q", msg)
	}
	if err := m.Write(id, "late"); err == nil {
		t.Fatal("write after kill must error")
	}
	_ = m.Prune()
}

func TestProcessUnknownIDErrors(t *testing.T) {
	m := NewProcessManager()
	if _, err := m.Poll("proc-999"); err == nil {
		t.Fatal("expected unknown-session error")
	}
	if _, err := m.Kill("proc-999"); err == nil {
		t.Fatal("expected unknown-session error")
	}
	if _, _, err := m.Log("proc-999", 0); err == nil {
		t.Fatal("expected unknown-session error")
	}
	if err := m.Write("proc-999", "x"); err == nil {
		t.Fatal("expected unknown-session error")
	}
}

func TestProcessToolDispatch(t *testing.T) {
	dir := t.TempDir()
	tool := &ProcessTool{AllowDirs: []string{dir}, Manager: NewProcessManager()}
	ctx := context.Background()
	id, err := tool.Execute(ctx, map[string]any{
		"action": "start", "command": "echo via-tool", "cwd": dir, "timeout_s": 30,
	})
	if err != nil || id == "" {
		t.Fatalf("start: id=%q err=%v", id, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := tool.Execute(ctx, map[string]any{"action": "poll", "id": id})
		if strings.Contains(out, "done") && strings.Contains(out, "via-tool") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never done: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	out, err := tool.Execute(ctx, map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("list = %q err=%v", out, err)
	}
	if _, err := tool.Execute(ctx, map[string]any{"action": "bogus"}); err == nil {
		t.Fatal("unknown action must error")
	}
	if _, err := tool.Execute(ctx, map[string]any{"action": "start", "command": "echo x", "cwd": "/nope-outside-allow"}); err == nil {
		t.Fatal("cwd outside allow must error")
	}
	// Timeout supervisor: 1s sleep with 1s timeout finishes on its own quickly;
	// use an infinite loop to prove the supervisor kills it.
	infID, err := tool.Execute(ctx, map[string]any{"action": "start", "command": "while true; do sleep 1; done", "timeout_s": 1})
	if err != nil {
		t.Fatalf("start infinite: %v", err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		out, _ := tool.Execute(ctx, map[string]any{"action": "poll", "id": infID})
		if strings.Contains(out, "done") {
			break
		}
		if time.Now().After(deadline) {
			_, _ = tool.Execute(ctx, map[string]any{"action": "kill", "id": infID})
			t.Fatalf("supervisor timeout did not fire: %q", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = os.Remove(filepath.Join(dir, "x"))
}
