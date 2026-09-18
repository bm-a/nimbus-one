package skills

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"nimbus-one/internal/tools"
)

// SkillExecTimeout bounds a single skill execution.
const SkillExecTimeout = 120 * time.Second

// skillOutputLimit caps captured skill output returned to the caller.
const skillOutputLimit = 32 * 1024

// ScriptTool exposes a Skill as a tools.Tool by running its run script
// (scripts/run.sh) or, failing that, the first ```sh fenced block in Body.
type ScriptTool struct {
	Skill *Skill
}

// Name returns the skill (tool) name.
func (t *ScriptTool) Name() string {
	if t.Skill == nil {
		return "skill"
	}
	return t.Skill.Name
}

// Description returns the skill description plus triggers.
func (t *ScriptTool) Description() string {
	if t.Skill == nil {
		return "skill"
	}
	d := t.Skill.Description
	if len(t.Skill.Triggers) > 0 {
		d += " (triggers: " + strings.Join(t.Skill.Triggers, ", ") + ")"
	}
	return d
}

// Parameters returns the skill-declared parameters.
func (t *ScriptTool) Parameters() map[string]tools.Param {
	if t.Skill == nil || t.Skill.Params == nil {
		return map[string]tools.Param{}
	}
	return t.Skill.Params
}

// Execute runs the skill script with args exposed as SKILL_ARG_<KEY> env vars.
func (t *ScriptTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Skill == nil {
		return "", fmt.Errorf("skills: nil skill")
	}
	if args == nil {
		args = map[string]any{}
	}
	script, isFile, err := t.resolveScript()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(t.Skill.Path)

	ctx, cancel := context.WithTimeout(ctx, SkillExecTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		if isFile {
			cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
		} else {
			cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
		}
	} else {
		if isFile {
			cmd = exec.CommandContext(ctx, "sh", script)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", script)
		}
	}
	cmd.Dir = dir
	env := os.Environ()
	env = append(env, "SKILL_NAME="+t.Skill.Name, "SKILL_DIR="+dir)
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "SKILL_ARG_"+envKey(k)+"="+fmt.Sprint(args[k]))
	}
	cmd.Env = env

	out, err := cmd.CombinedOutput()
	s := truncateOutput(string(out), skillOutputLimit)
	if ctx.Err() == context.DeadlineExceeded {
		return s, fmt.Errorf("skills: %q timed out after %s", t.Skill.Name, SkillExecTimeout)
	}
	if err != nil {
		return s, fmt.Errorf("skills: %q failed: %w", t.Skill.Name, err)
	}
	return s, nil
}

// resolveScript locates the runnable script for the skill.
func (t *ScriptTool) resolveScript() (script string, isFile bool, err error) {
	dir := filepath.Dir(t.Skill.Path)
	cand := filepath.Join(dir, "scripts", "run.sh")
	if st, serr := os.Stat(cand); serr == nil && !st.IsDir() {
		return cand, true, nil
	}
	if runtime.GOOS == "windows" {
		if ps := filepath.Join(dir, "scripts", "run.ps1"); func() bool {
			st, serr := os.Stat(ps)
			return serr == nil && !st.IsDir()
		}() {
			return ps, true, nil
		}
	}
	if block := extractShBlock(t.Skill.Body); block != "" {
		return block, false, nil
	}
	return "", false, fmt.Errorf("skills: %q has no runnable script (no scripts/run.sh or ```sh block)", t.Skill.Name)
}

// extractShBlock returns the first ```sh (bash/shell, or bare) fenced block.
func extractShBlock(body string) string {
	var fallback string
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "```") {
			continue
		}
		lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "```")))
		// Collect until closing fence.
		var blk []string
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" || strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
				break
			}
			blk = append(blk, lines[j])
		}
		if j >= len(lines) {
			break // unterminated fence
		}
		content := strings.Trim(strings.Join(blk, "\n"), "\n")
		if content == "" {
			i = j
			continue
		}
		switch lang {
		case "sh", "bash", "shell":
			return content
		case "":
			if fallback == "" {
				fallback = content
			}
		}
		i = j
	}
	return fallback
}

// envKey uppercases a key and replaces non [A-Z0-9_] runes with '_'.
func envKey(k string) string {
	k = strings.ToUpper(strings.TrimSpace(k))
	var b strings.Builder
	for _, r := range k {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		return "ARG"
	}
	return s
}

func truncateOutput(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n...[truncated %d bytes]", len(s)-limit)
}

// RegisterAll registers every skill in reg as a tool in toolsReg.
func RegisterAll(reg *SkillRegistry, toolsReg *tools.Registry) {
	if reg == nil || toolsReg == nil {
		return
	}
	for _, n := range reg.Names() {
		toolsReg.Register(&ScriptTool{Skill: reg.Skills[n]})
	}
}
