package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGo(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	if err := os.WriteFile(good, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs := Check(good)
	if len(rs) != 2 {
		t.Fatalf("go checks = %d, want 2", len(rs))
	}
	for _, r := range rs {
		if r.Skipped != "" {
			t.Skipf("%s skipped: %s", r.Checker, r.Skipped)
		}
		if !r.OK {
			t.Fatalf("%s on valid file: %+v", r.Checker, r)
		}
	}
	bad := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(bad, []byte("package main\n\nfunc main( {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, r := range Check(bad) {
		if r.Skipped != "" {
			continue
		}
		if !r.OK {
			failed = true
		}
	}
	if !failed {
		t.Fatal("broken go file must fail at least one checker")
	}
}

func TestCheckUnknownExt(t *testing.T) {
	if rs := Check("notes.txt"); len(rs) != 0 {
		t.Fatalf("unknown ext checks = %v, want none", rs)
	}
}

func TestReport(t *testing.T) {
	r := Report([]Result{{Checker: "gofmt", OK: true}, {Checker: "x", Skipped: "no bin"}})
	if !strings.Contains(r, "clean") || !strings.Contains(r, "skipped") {
		t.Fatalf("report = %q", r)
	}
}
