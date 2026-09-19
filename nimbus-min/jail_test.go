package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testJail(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	workspaceRoot = "" // reset global between tests
	if err := initJail(dir); err != nil {
		t.Fatalf("initJail: %v", err)
	}
	return dir
}

func TestInitJailRejectsBadRoots(t *testing.T) {
	workspaceRoot = ""
	if err := initJail(""); err == nil {
		t.Fatal("empty root must error")
	}
	if err := initJail(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("missing dir must error")
	}
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := initJail(f); err == nil {
		t.Fatal("file (not dir) must error")
	}
}

func TestResolveTraversalRejected(t *testing.T) {
	testJail(t)
	for _, p := range []string{
		"../../etc/passwd",
		"a/../../../etc/passwd",
		"a/b/../../../../..",
		"..",
		"../escape.txt",
		"sub/../../escape.txt",
	} {
		if _, err := resolve(p); err == nil {
			t.Fatalf("traversal %q must be rejected", p)
		}
	}
}

func TestResolveAbsoluteRejected(t *testing.T) {
	dir := testJail(t)
	// Even an absolute path pointing INSIDE the workspace is rejected:
	// callers must use workspace-relative paths, no exceptions.
	if _, err := resolve(filepath.Join(dir, "notes.md")); err == nil {
		t.Fatal("absolute path must be rejected even inside workspace")
	}
	if _, err := resolve("/etc/passwd"); err == nil {
		t.Fatal("/etc/passwd must be rejected")
	}
}

func TestResolveValidPaths(t *testing.T) {
	dir := testJail(t)
	for _, p := range []string{".", "notes/todo.md", "a/./b", "a//b"} {
		got, err := resolve(p)
		if err != nil {
			t.Fatalf("valid path %q rejected: %v", p, err)
		}
		if !strings.HasPrefix(got, dir) {
			t.Fatalf("resolved %q outside workspace", got)
		}
	}
	if _, err := resolve(""); err == nil {
		t.Fatal("empty path must error")
	}
	if _, err := resolve("a\x00b"); err == nil {
		t.Fatal("NUL byte must error")
	}
}

func TestResolveSymlinkEscape(t *testing.T) {
	dir := testJail(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink inside the workspace pointing outside.
	link := filepath.Join(dir, "evil-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve("evil-link/secret.txt"); err == nil {
		t.Fatal("symlink escape must be rejected")
	}
	// Symlink to a file inside the workspace is fine.
	inner := filepath.Join(dir, "inner.txt")
	if err := os.WriteFile(inner, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	okLink := filepath.Join(dir, "ok-link")
	if err := os.Symlink(inner, okLink); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve("ok-link"); err != nil {
		t.Fatalf("inner symlink must resolve: %v", err)
	}
	// Write-target whose parent dir is a symlink outward.
	sub := filepath.Join(dir, "sub")
	if err := os.Symlink(outside, sub); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve("sub/newfile.txt"); err == nil {
		t.Fatal("write through outward-symlinked dir must be rejected")
	}
}

func TestCheckShellText(t *testing.T) {
	bad := []string{
		"cat /etc/passwd",
		"ls /",
		"cat ../../etc/passwd",
		"cd / && cat etc/passwd",
		"sh -c 'cat /etc/shadow'",
		"cp a.txt /tmp/b.txt",
		"cat --file=/etc/passwd",
		"echo hi > /tmp/x",
	}
	for _, c := range bad {
		if err := checkShellText(c); err == nil {
			t.Fatalf("shell escape %q must be rejected", c)
		}
	}
	good := []string{
		"ls -la",
		"go test ./...",
		"cat notes/todo.md",
		"grep -r \"TODO\" .",
		"echo hello world",
		"curl https://example.com/x",
		"npm --version",
	}
	for _, c := range good {
		if err := checkShellText(c); err != nil {
			t.Fatalf("legit command %q rejected: %v", c, err)
		}
	}
}
