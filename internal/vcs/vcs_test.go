package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectRepo(t *testing.T) {
	dir := gitTestRepo(t)
	info := Detect(dir)
	if !info.IsRepo || info.Branch == "" || info.Clean {
		t.Fatalf("info = %+v, want repo with dirty tree", info)
	}
	if got := Detect(t.TempDir()); got.IsRepo {
		t.Fatal("empty dir must not detect as repo")
	}
}

func TestStatusDiffLog(t *testing.T) {
	dir := gitTestRepo(t)
	st, err := Status(dir)
	if err != nil || !strings.Contains(st, "f.txt") {
		t.Fatalf("status = %q, err = %v", st, err)
	}
	d, err := Diff(dir, 0)
	if err != nil || !strings.Contains(d, "-a") || !strings.Contains(d, "+b") {
		t.Fatalf("diff = %q, err = %v", d, err)
	}
	l, err := Log(dir, ".", 5)
	if err != nil || !strings.Contains(l, "init") {
		t.Fatalf("log = %q, err = %v", l, err)
	}
}
