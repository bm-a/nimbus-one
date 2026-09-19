package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func patchSetup(t *testing.T) (*PatchTool, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &PatchTool{AllowDirs: []string{dir}}, dir
}

func TestPatch_AddUpdateDelete(t *testing.T) {
	p, dir := patchSetup(t)
	out, err := p.Execute(context.Background(), map[string]any{"patch": `*** Begin Patch
*** Add File: new.txt
+hello
+world
*** Update File: a.txt
 l1
-l2
+L2!
 l3
*** End Patch`})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "2 operation(s)") {
		t.Fatalf("receipt = %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "new.txt")); string(b) != "hello\nworld\n" {
		t.Fatalf("new.txt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "l1\nL2!\nl3\n" {
		t.Fatalf("a.txt = %q", b)
	}
	out, err = p.Execute(context.Background(), map[string]any{"patch": "*** Begin Patch\n*** Delete File: new.txt\n*** End Patch"})
	if err != nil || !strings.Contains(out, "delete") {
		t.Fatalf("delete: out=%q err=%v", out, err)
	}
}

func TestPatch_Rejects(t *testing.T) {
	p, _ := patchSetup(t)
	for name, env := range map[string]string{
		"no begin":      "*** Update File: a.txt\n l1\n",
		"no end":        "*** Begin Patch\n*** Delete File: a.txt\n",
		"unknown":       "*** Begin Patch\n*** Frobnicate: a.txt\n*** End Patch",
		"missing":       "*** Begin Patch\n*** Update File: a.txt\n nope\n*** End Patch",
		"ambiguous":     "*** Begin Patch\n*** Update File: a.txt\n\n*** End Patch",
		"outside":       "*** Begin Patch\n*** Add File: ../evil.txt\n+x\n*** End Patch",
		"add exists":    "*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch",
		"trailing junk": "*** Begin Patch\n*** Delete File: a.txt\n*** End Patch\njunk",
	} {
		if _, err := p.Execute(context.Background(), map[string]any{"patch": env}); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}
