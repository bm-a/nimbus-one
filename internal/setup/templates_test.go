package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestTemplatesMatchRepoFiles guards against drift between the embedded
// constants (shipped in the binary) and default_workspace/ (for humans).
func TestTemplatesMatchRepoFiles(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "default_workspace")
	cases := map[string]string{
		"SOUL.md":               DefaultSoul,
		"USER.md":               DefaultUser,
		"MEMORY.md":             DefaultMemory,
		"HEARTBEAT.md":          DefaultHeartbeat,
		"skills/hello/SKILL.md": DefaultHelloSkill,
	}
	for rel, want := range cases {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if string(b) != want {
			t.Errorf("%s drifted from embedded constant (fix templates.go or the workspace file)", rel)
		}
	}
}
