package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpencodeMissingBinGuidesFileFallback(t *testing.T) {
	// Force unresolved binary: empty Bin, bogus PATH, no OPENCODE_BIN.
	t.Setenv("OPENCODE_BIN", "")
	t.Setenv("PATH", t.TempDir())
	tool := &OpencodeTool{Dir: t.TempDir(), Timeout: time.Minute}
	out, err := tool.Execute(context.Background(), map[string]any{"task": "fix everything"})
	if err != nil {
		t.Fatalf("missing binary must guide, not error: %v", err)
	}
	if !strings.Contains(out, "pulling the files") {
		t.Fatalf("must direct file fallback: %q", out)
	}
}

func TestOpencodeEmptyTaskErrors(t *testing.T) {
	tool := &OpencodeTool{Bin: "opencode"}
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("empty task must error")
	}
}

func TestOpencodeDirJailed(t *testing.T) {
	dir := t.TempDir()
	tool := &OpencodeTool{Bin: "opencode", Dir: dir, AllowDirs: []string{dir}, Timeout: time.Minute}
	if _, err := tool.Execute(context.Background(), map[string]any{"task": "x", "dir": "../escape"}); err == nil {
		t.Fatal("dir escape must be rejected")
	}
}

func TestOpencodeFakeBinReturnsOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	body := "#!/bin/sh\necho '{\"type\": \"text\", \"text\": \"delegated-done\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := &OpencodeTool{Bin: script, Dir: dir, Timeout: 10 * time.Second}
	out, err := tool.Execute(context.Background(), map[string]any{"task": "do it"})
	if err != nil {
		t.Fatalf("fake delegation: %v", err)
	}
	if !strings.Contains(out, "delegated-done") {
		t.Fatalf("out = %q", out)
	}
}
