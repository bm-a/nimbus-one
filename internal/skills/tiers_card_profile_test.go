package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nimbus-one/internal/tools"
)

func writeTierSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTieredRegistry_FirstWinsAndShadowWarning(t *testing.T) {
	root := t.TempDir()
	bundled := filepath.Join(root, "bundled", "dup")
	workspace := filepath.Join(root, "workspace", "dup")
	extra := filepath.Join(root, "bundled", "solo")
	writeTierSkill(t, bundled, "dup", "bundled copy")
	writeTierSkill(t, workspace, "dup", "workspace copy")
	writeTierSkill(t, extra, "solo", "only here")

	tr := NewTieredRegistry(nil)
	tr.AddTier("bundled", filepath.Join(root, "bundled"))
	tr.AddTier("workspace", filepath.Join(root, "workspace"))
	warnings, err := tr.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got, ok := tr.Get("dup")
	if !ok {
		t.Fatal("dup skill missing")
	}
	if got.Description != "bundled copy" {
		t.Fatalf("dup = %q, want first-wins bundled copy", got.Description)
	}
	if _, ok := tr.Get("solo"); !ok {
		t.Fatal("solo skill missing")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "dup") || !strings.Contains(warnings[0], "shadowed") {
		t.Fatalf("warnings = %v, want one shadow warning for dup", warnings)
	}
}

func TestTieredRegistry_MissingTierIsWarning(t *testing.T) {
	tr := NewTieredRegistry(nil)
	tr.AddTier("ghost", filepath.Join(t.TempDir(), "nope"))
	warnings, err := tr.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ghost") {
		t.Fatalf("warnings = %v, want missing-tier warning", warnings)
	}
	if got := tr.Tiers(); len(got) != 1 || got[0] != "ghost" {
		t.Fatalf("Tiers = %v, want [ghost]", got)
	}
}

func TestParseCard(t *testing.T) {
	dir := t.TempDir()
	card := "# Card\n\n## Publisher\nAcme Corp\n\n## License\nMIT\n\n## Version\n1.2.0\n\n" +
		"## Risks\nRuns shell commands.\nSecond line.\n\n## Use-Case\nSummarize logs.\n"
	if err := os.WriteFile(filepath.Join(dir, "skill-card.md"), []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := ParseCard(dir)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if c.Publisher != "Acme Corp" || c.License != "MIT" || c.Version != "1.2.0" {
		t.Fatalf("card = %+v, want publisher/license/version", c)
	}
	if !strings.Contains(c.Risks, "shell") || c.UseCase != "Summarize logs." {
		t.Fatalf("risks/usecase = %q/%q", c.Risks, c.UseCase)
	}
	if _, err := ParseCard(t.TempDir()); err == nil {
		t.Fatal("missing card: got nil error, want error")
	}
}

func TestLoadDir_PopulatesCard(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "withcard", "SKILL.md"), "withcard", "Has a card")
	card := "## Publisher\nAcme\n## License\nMIT\n"
	if err := os.WriteFile(filepath.Join(root, "withcard", "skill-card.md"), []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(root, "nocard", "SKILL.md"), "nocard", "No card")

	reg := NewSkillRegistry(nil)
	if err := reg.LoadDir(root); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	sk, ok := reg.Get("withcard")
	if !ok {
		t.Fatal("withcard missing")
	}
	if sk.Card == nil || sk.Card.Publisher != "Acme" {
		t.Fatalf("withcard.Card = %+v, want Acme publisher", sk.Card)
	}
	sk, ok = reg.Get("nocard")
	if !ok {
		t.Fatal("nocard missing")
	}
	if sk.Card != nil {
		t.Fatalf("nocard.Card = %+v, want nil", sk.Card)
	}
}

type profileStubTool struct{ name string }

func (s *profileStubTool) Name() string { return s.name }

func (s *profileStubTool) Description() string { return "stub " + s.name }

func (s *profileStubTool) Parameters() map[string]tools.Param { return nil }

func (s *profileStubTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return s.name + "-ok", nil
}

func TestProfile_Filter(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&profileStubTool{"read"})
	reg.Register(&profileStubTool{"bash"})
	reg.Register(&profileStubTool{"write"})

	p := &Profile{Name: "readonly"}
	kept, warnings := p.Filter(reg, []string{"read", "nope", "read"})
	if kept.Count() != 1 {
		t.Fatalf("kept count = %d, want 1", kept.Count())
	}
	if _, ok := kept.Get("read"); !ok {
		t.Fatal("read should be kept")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "nope") {
		t.Fatalf("warnings = %v, want unknown-tool warning for nope", warnings)
	}
	if reg.Count() != 3 {
		t.Fatalf("input registry mutated: count = %d, want 3", reg.Count())
	}
	// Nil input: empty registry + warnings for every name.
	kept, warnings = p.Filter(nil, []string{"read"})
	if kept.Count() != 0 || len(warnings) != 1 {
		t.Fatalf("nil input gave count=%d warnings=%v, want 0 + 1", kept.Count(), warnings)
	}
}
