package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFileOpenClawMinimal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "myskill", "SKILL.md")
	writeSkillFile(t, p, `---
name: my-skill
description: Does things well
---
# my-skill
Body here.
`)
	sk, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if sk.Name != "my-skill" {
		t.Fatalf("Name = %q, want my-skill", sk.Name)
	}
	if sk.Description != "Does things well" {
		t.Fatalf("Description = %q", sk.Description)
	}
	if !strings.Contains(sk.Body, "Body here") {
		t.Fatalf("Body = %q, want it to contain body text", sk.Body)
	}
	if sk.Path != p {
		t.Fatalf("Path = %q, want %q", sk.Path, p)
	}
}

func TestParseFileHermesStrict(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hermes", "SKILL.md")
	writeSkillFile(t, p, `---
name: hermes
description: Strict skill
version: 1.2.3
author: tester
platforms: [linux, darwin]
---
Body content.
`)
	sk, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if sk.Name != "hermes" {
		t.Fatalf("Name = %q", sk.Name)
	}
	if sk.Description != "Strict skill" {
		t.Fatalf("Description = %q", sk.Description)
	}
	if sk.Version != "1.2.3" {
		t.Fatalf("Version = %q", sk.Version)
	}
	if sk.Author != "tester" {
		t.Fatalf("Author = %q", sk.Author)
	}
}

func TestParseFileNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "fallbackname")
	p := filepath.Join(skillDir, "SKILL.md")
	writeSkillFile(t, p, "# Fancy Heading\n\nSome body text.\n")
	sk, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if sk.Name != "fallbackname" {
		t.Fatalf("Name = %q, want fallbackname (dir name)", sk.Name)
	}
	if sk.Description != "Fancy Heading" {
		t.Fatalf("Description = %q, want first heading", sk.Description)
	}
}

func TestParseFileTriggersCSV(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s", "SKILL.md")
	writeSkillFile(t, p, `---
name: trigcsv
description: d
triggers: alpha, beta, gamma
---
Body.
`)
	sk, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(sk.Triggers) != 3 || sk.Triggers[0] != "alpha" || sk.Triggers[1] != "beta" || sk.Triggers[2] != "gamma" {
		t.Fatalf("Triggers = %v, want [alpha beta gamma]", sk.Triggers)
	}
}

func TestParseFileTriggersList(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s", "SKILL.md")
	writeSkillFile(t, p, `---
name: triglist
description: d
triggers:
  - alpha
  - beta
---
Body.
`)
	sk, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(sk.Triggers) != 2 || sk.Triggers[0] != "alpha" || sk.Triggers[1] != "beta" {
		t.Fatalf("Triggers = %v, want [alpha beta]", sk.Triggers)
	}
}

func TestParseDirMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ParseDir(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatal("ParseDir on missing dir should error")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDir(empty); err == nil {
		t.Fatal("ParseDir on dir without SKILL.md should error")
	}
}

func TestParseFileMissingErrors(t *testing.T) {
	if _, err := ParseFile(filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Fatal("ParseFile on missing file should error")
	}
}

func TestParseDirSuccess(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "cool")
	writeSkillFile(t, filepath.Join(skillDir, "SKILL.md"), `---
name: cool
description: Cool skill
---
Body.
`)
	sk, err := ParseDir(skillDir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if sk.Name != "cool" {
		t.Fatalf("Name = %q", sk.Name)
	}
}
