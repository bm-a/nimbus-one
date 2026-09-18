package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceSaveLoadRoundtrip(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	content := "# soul\nhello world"
	if err := w.Save(SoulFile, content); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := w.Load(SoulFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != content {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, content)
	}
}

func TestWorkspaceLoadMissingReturnsEmpty(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	got, err := w.Load("DOES_NOT_EXIST.md")
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string for missing file, got %q", got)
	}
}

func TestWorkspaceAppendAddsTimestampedEntry(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	if err := w.Save(MemoryFile, "existing"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := w.Append(MemoryFile, "new entry"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := w.Load(MemoryFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got, "existing") {
		t.Fatalf("expected original content preserved, got %q", got)
	}
	if !strings.Contains(got, "new entry") {
		t.Fatalf("expected appended entry, got %q", got)
	}
	if !strings.Contains(got, "## ") {
		t.Fatalf("expected timestamp header '## ', got %q", got)
	}
}

func TestWorkspaceAppendCreatesFile(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	if err := w.Append(MemoryFile, "first entry"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := w.Load(MemoryFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got, "first entry") {
		t.Fatalf("expected entry, got %q", got)
	}
}

func TestWorkspaceEnsureDefaultsDoesNotOverwrite(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	// Pre-existing file must be preserved.
	if err := w.Save(SoulFile, "custom soul"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := w.EnsureDefaults("d-soul", "d-user", "d-memory", "d-heartbeat"); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	got, err := w.Load(SoulFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "custom soul" {
		t.Fatalf("EnsureDefaults overwrote existing file: got %q", got)
	}
	// Missing files should be created with defaults.
	for _, tc := range []struct {
		name string
		want string
	}{
		{UserFile, "d-user"},
		{MemoryFile, "d-memory"},
		{HeartbeatFile, "d-heartbeat"},
	} {
		got, err := w.Load(tc.name)
		if err != nil {
			t.Fatalf("Load %s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("file %s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestWorkspaceSaveSetsPerms(t *testing.T) {
	w := Workspace{Dir: t.TempDir()}
	if err := w.Save(UserFile, "x"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(filepath.Join(w.Dir, UserFile))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("expected 0644, got %o", fi.Mode().Perm())
	}
}
