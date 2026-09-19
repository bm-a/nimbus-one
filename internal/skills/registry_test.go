package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, path, name, desc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody for " + name + ".\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryLoadDirFlatAndNested(t *testing.T) {
	root := t.TempDir()
	// Flat layout: <root>/flat/SKILL.md
	writeSkill(t, filepath.Join(root, "flat", "SKILL.md"), "flat", "Flat skill")
	// Two-level layout: <root>/cat/nested/SKILL.md
	writeSkill(t, filepath.Join(root, "cat", "nested", "SKILL.md"), "nested", "Nested skill")

	reg := NewSkillRegistry(nil)
	if err := reg.LoadDir(root); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if _, ok := reg.Get("flat"); !ok {
		t.Fatal("expected flat skill to be loaded")
	}
	if _, ok := reg.Get("nested"); !ok {
		t.Fatal("expected nested skill to be loaded")
	}
	if got := len(reg.Names()); got != 2 {
		t.Fatalf("expected 2 skills, got %d (%v)", got, reg.Names())
	}
}

func TestRegistryCapabilitiesPromptContainsNames(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "flat", "SKILL.md"), "flat", "Flat skill")
	writeSkill(t, filepath.Join(root, "cat", "nested", "SKILL.md"), "nested", "Nested skill")

	reg := NewSkillRegistry(nil)
	if err := reg.LoadDir(root); err != nil {
		t.Fatal(err)
	}
	prompt := reg.CapabilitiesPrompt()
	if !strings.Contains(prompt, "flat") {
		t.Fatalf("CapabilitiesPrompt missing flat: %q", prompt)
	}
	if !strings.Contains(prompt, "nested") {
		t.Fatalf("CapabilitiesPrompt missing nested: %q", prompt)
	}
}

func TestRegistryLoadDirMissingRootNoError(t *testing.T) {
	reg := NewSkillRegistry(nil)
	if err := reg.LoadDir(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatalf("LoadDir on missing root should return nil, got %v", err)
	}
}

func TestRegistryVerboseExcerptCapped(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "v", "SKILL.md"), "verb", "Verbose skill")
	reg := NewSkillRegistry(nil)
	if err := reg.LoadDir(root); err != nil {
		t.Fatal(err)
	}
	v := reg.CapabilitiesPromptVerbose()
	if !strings.Contains(v, "verb") || !strings.Contains(v, "use:") {
		t.Fatalf("verbose prompt must include name + body excerpt: %q", v)
	}
	if len(v) > 2000 {
		t.Fatalf("verbose prompt uncapped: %d bytes", len(v))
	}
}
