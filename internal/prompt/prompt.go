// Package prompt assembles the system prompt from data, not scattered
// string building: identity sections + environment facts + model-variant
// addendum + skills/MCP capability text.
//
// The environment block exists because the emulator proved the model
// guesses paths and working directories without it. The variant exists
// because different model families respond to different instruction
// styles (OpenCode ships per-family prompts for the same reason).
package prompt

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Variant selects the instruction style.
type Variant string

// Variants.
const (
	Default Variant = "default"
	GPT     Variant = "gpt"
)

// VariantFor maps a model-profile variant name to a Variant.
func VariantFor(name string) Variant {
	if name == "gpt" {
		return GPT
	}
	return Default
}

// Env describes the runtime facts injected into every prompt.
type Env struct {
	Workdir   string
	SkillsDir string
	ToolNames []string
	IsGitRepo bool
}

// Block renders the <env> facts plus operating discipline.
func (e Env) Block() string {
	var b strings.Builder
	b.WriteString("## Environment (facts, not suggestions)\n")
	b.WriteString("- workspace: " + e.Workdir + " (your files live here; relative tool paths like . or notes/todo.md resolve inside it)\n")
	b.WriteString("- skills: " + e.SkillsDir + "\n")
	git := "no"
	if e.IsGitRepo {
		git = "yes"
	}
	fmt.Fprintf(&b, "- git repo: %s\n", git)
	fmt.Fprintf(&b, "- platform: %s/%s, shell: sh, today: %s\n", runtime.GOOS, runtime.GOARCH, time.Now().Format("2006-01-02"))
	b.WriteString("- tools available: " + strings.Join(e.ToolNames, ", ") + "\n")
	b.WriteString("- tool discipline: when asked about files, ALWAYS attempt the tool call first (relative paths resolve inside the workspace and always succeed there). Never tell the user something is outside allowed paths without trying — a failed call returns the exact reason, which you then report verbatim.\n")
	return b.String()
}

// Addendum returns the model-variant instruction style, empty for default.
func Addendum(v Variant) string {
	if v == GPT {
		return "## Instruction style\n" +
			"- Prefer the apply_patch tool for file changes (exact envelope); avoid edit for multi-file work.\n" +
			"- Batch independent tool calls in one block; confirm each result before the next dependent step.\n" +
			"- Keep code changes minimal and surgical; do not refactor beyond the request.\n"
	}
	return ""
}

// Assemble joins the parts with blank-line separators, skipping empties.
func Assemble(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, strings.Trim(p, "\n"))
		}
	}
	return strings.Join(kept, "\n\n")
}
