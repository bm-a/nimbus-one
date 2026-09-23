package llm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFakeBin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-opencode")
	// Resolve sh for the shebang (Termux has no /bin/sh or /usr/bin/env).
	sh, err := exec.LookPath("sh")
	if err != nil || strings.TrimSpace(sh) == "" {
		sh = "/bin/sh"
	}
	content := "#!" + sh + "\n" + body + "\n"
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("chmod fake bin: %v", err)
	}
	return p
}

func TestSidecar_NonexistentBinaryReturnsError(t *testing.T) {
	s := &Sidecar{Bin: "/nonexistent-nimbus-one-opencode-xyz", Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := s.Run(ctx, "hello", nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sidecar") {
		t.Fatalf("err = %v, want sidecar mention", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("nonexistent binary took too long; want fast failure")
	}
}

func TestSidecar_FakeScriptEmitsTextEvent(t *testing.T) {
	bin := writeFakeBin(t, `echo '{"type": "text", "text": "hello from sidecar"}'`)
	s := &Sidecar{Bin: bin, Timeout: 10 * time.Second}
	var events []OpEvent
	out, err := s.Run(context.Background(), "do something", func(e OpEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out) != "hello from sidecar" {
		t.Fatalf("out = %q, want %q", out, "hello from sidecar")
	}
	if len(events) == 0 {
		t.Fatal("want at least one event")
	}
	found := false
	for _, e := range events {
		if e.Type == "text" && strings.Contains(e.Text, "hello from sidecar") {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v, want text event with payload", events)
	}
}

func TestSidecar_Timeout(t *testing.T) {
	bin := writeFakeBin(t, `sleep 10`)
	s := &Sidecar{Bin: bin, Timeout: 200 * time.Millisecond}
	_, err := s.Run(context.Background(), "slow prompt", nil)
	if err == nil {
		t.Fatal("want timeout error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("err = %v, want timeout mention", err)
	}
}

func TestSidecar_EmptyPromptReturnsError(t *testing.T) {
	s := &Sidecar{Bin: "/nonexistent-nimbus-one-opencode-xyz", Timeout: 5 * time.Second}
	_, err := s.Run(context.Background(), "   ", nil)
	if err == nil {
		t.Fatal("want error for empty prompt, got nil")
	}
}

func TestSidecar_FastExitOutputNotDropped(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)

	bin := writeFakeBin(t, `echo '{"type": "text", "text": "fast-exit"}'`)
	s := &Sidecar{Bin: bin, Timeout: 10 * time.Second}
	for i := 0; i < 100; i++ {
		out, err := s.Run(context.Background(), "do something", nil)
		if err != nil {
			t.Fatalf("Run[%d]: %v", i, err)
		}
		if strings.TrimSpace(out) != "fast-exit" {
			t.Fatalf("Run[%d] out = %q, want %q", i, out, "fast-exit")
		}
	}
}
