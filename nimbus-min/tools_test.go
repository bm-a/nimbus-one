package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolReadWriteRoundtrip(t *testing.T) {
	dir := testJail(t)
	if _, err := toolWrite("hello.txt", "hi there"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := toolRead("hello.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "hi there" {
		t.Fatalf("roundtrip = %q", got)
	}
	_ = dir
}

func TestToolReadMissing(t *testing.T) {
	testJail(t)
	if _, err := toolRead("nope.txt"); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestToolWriteNestedAndTraversal(t *testing.T) {
	testJail(t)
	if _, err := toolWrite("a/b/c.txt", "deep"); err != nil {
		t.Fatalf("nested write: %v", err)
	}
	for _, p := range []string{"../evil.txt", "/abs.txt", "a/../../evil.txt"} {
		if _, err := toolWrite(p, "x"); err == nil {
			t.Fatalf("write %q must be rejected", p)
		}
		if _, err := toolRead(p); err == nil {
			t.Fatalf("read %q must be rejected", p)
		}
	}
}

func TestToolEdit(t *testing.T) {
	testJail(t)
	if _, err := toolWrite("f.txt", "l1\nl2\nl3\n"); err != nil {
		t.Fatal(err)
	}
	out, err := toolEdit("f.txt", "l2", "L2!")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(out, "1 change") {
		t.Fatalf("receipt = %q", out)
	}
	got, _ := toolRead("f.txt")
	if got != "l1\nL2!\nl3\n" {
		t.Fatalf("edited = %q", got)
	}
	// No-op edit returns a receipt, not an error.
	if _, err := toolEdit("f.txt", "L2!", "L2!"); err != nil {
		t.Fatalf("no-op edit: %v", err)
	}
	// Missing text errors with a snippet.
	if _, err := toolEdit("f.txt", "zzz", "q"); err == nil {
		t.Fatal("missing oldText must error")
	}
	// Ambiguous text errors.
	if _, err := toolWrite("g.txt", "x\nx\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := toolEdit("g.txt", "x", "y"); err == nil {
		t.Fatal("ambiguous oldText must error")
	}
	// Empty search errors.
	if _, err := toolEdit("f.txt", "", "y"); err == nil {
		t.Fatal("empty oldText must error")
	}
	// Nonexistent file errors.
	if _, err := toolEdit("nope.txt", "a", "b"); err == nil {
		t.Fatal("edit of missing file must error")
	}
}

func TestToolList(t *testing.T) {
	dir := testJail(t)
	if _, err := toolWrite("b.txt", "x"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := toolList(".")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "b.txt") || !strings.Contains(out, "sub/") {
		t.Fatalf("list = %q", out)
	}
	if _, err := toolList(".."); err == nil {
		t.Fatal("list .. must be rejected")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err = toolList("empty")
	if err != nil || out != "(empty folder)" {
		t.Fatalf("empty list = %q, %v", out, err)
	}
}

func TestToolShellBasic(t *testing.T) {
	testJail(t)
	out, err := toolShell("echo hello", 10)
	if err != nil {
		t.Fatalf("shell echo: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("shell out = %q", out)
	}
}

func TestToolShellCwdLocked(t *testing.T) {
	dir := testJail(t)
	out, err := toolShell("pwd", 10)
	if err != nil {
		t.Fatalf("pwd: %v", err)
	}
	if strings.TrimSpace(out) != dir {
		t.Fatalf("shell cwd = %q, want workspace %q", strings.TrimSpace(out), dir)
	}
}

func TestToolShellFailureAndTimeout(t *testing.T) {
	testJail(t)
	if _, err := toolShell("exit 3", 10); err == nil {
		t.Fatal("failing command must error")
	}
	if _, err := toolShell("sleep 5", 1); err == nil {
		t.Fatal("over-time command must error")
	}
}

func TestToolShellEscapeRejected(t *testing.T) {
	testJail(t)
	for _, c := range []string{"cat /etc/passwd", "cd /", "cat ../../etc/passwd"} {
		if _, err := toolShell(c, 10); err == nil {
			t.Fatalf("shell %q must be rejected before execution", c)
		}
	}
	if _, err := toolShell("", 10); err == nil {
		t.Fatal("empty command must error")
	}
}
