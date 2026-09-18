package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptToolExecuteRunSh(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "echoskill")
	scriptDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillBody := "---\nname: echo-skill\ndescription: Echo skill\n---\nBody.\n"
	if err := os.WriteFile(skillPath, []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
	runSh := "#!/bin/sh\necho \"got:$SKILL_ARG_FOO\"\n"
	if err := os.WriteFile(filepath.Join(scriptDir, "run.sh"), []byte(runSh), 0o755); err != nil {
		t.Fatal(err)
	}

	sk, err := ParseFile(skillPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	tool := &ScriptTool{Skill: sk}
	out, err := tool.Execute(context.Background(), map[string]any{"foo": "bar123"})
	if err != nil {
		t.Fatalf("Execute: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "bar123") {
		t.Fatalf("output %q does not contain arg bar123", out)
	}
}

func TestScriptToolInlineShBlockFallback(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "inlineskill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	content := "---\nname: inline-skill\ndescription: Inline skill\n---\nRun this:\n```sh\necho inline-ok-$SKILL_ARG_FOO\n```\n"
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, err := ParseFile(skillPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	tool := &ScriptTool{Skill: sk}
	out, err := tool.Execute(context.Background(), map[string]any{"foo": "zz"})
	if err != nil {
		t.Fatalf("Execute: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "inline-ok-zz") {
		t.Fatalf("output %q does not contain inline-ok-zz", out)
	}
}

func TestScriptToolNoScriptErrors(t *testing.T) {
	sk := &Skill{Name: "noscript", Description: "d", Path: filepath.Join(t.TempDir(), "SKILL.md"), Body: "no code here"}
	tool := &ScriptTool{Skill: sk}
	if _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatal("expected error when skill has no runnable script")
	}
}
