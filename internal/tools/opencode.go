package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"nimbus-one/internal/llm"
)

// OpencodeTool delegates heavy repository-wide coding and refactoring to the
// OpenCode sidecar: either OpenCode handles it directly, or the agent pulls
// the files and fixes them with its own tools. There is no third path.
type OpencodeTool struct {
	// Bin is the opencode binary. Empty = auto-resolve via OPENCODE_BIN or PATH.
	Bin string
	// Dir is the working directory for the delegated task.
	Dir string
	// AllowDirs scopes the dir argument; empty means no restriction.
	AllowDirs []string
	Model     string
	Timeout   time.Duration
}

func (t *OpencodeTool) Name() string { return "opencode" }

func (t *OpencodeTool) Description() string {
	return "Delegate a large multi-file coding/refactor task to OpenCode headless " +
		"(`opencode run --format json`). Use for repo-wide changes; use read/write/bash " +
		"directly for small targeted fixes."
}

func (t *OpencodeTool) Parameters() map[string]Param {
	return map[string]Param{
		"task": {Type: "string", Description: "Full task description for OpenCode.", Required: true},
		"dir":  {Type: "string", Description: "Working directory (defaults to tool Dir)."},
	}
}

func (t *OpencodeTool) bin() string {
	if strings.TrimSpace(t.Bin) != "" {
		return t.Bin
	}
	if env := strings.TrimSpace(os.Getenv("OPENCODE_BIN")); env != "" {
		return env
	}
	if p, err := exec.LookPath("opencode"); err == nil {
		return p
	}
	return ""
}

func (t *OpencodeTool) timeout() time.Duration {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return 10 * time.Minute
}

// Execute runs the delegation, or explains exactly how to proceed without it.
func (t *OpencodeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	task, _ := args["task"].(string)
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("opencode: missing required argument %q", "task")
	}
	dir := t.Dir
	if d, _ := args["dir"].(string); strings.TrimSpace(d) != "" {
		dir = d
	}
	if dir != "" && len(t.AllowDirs) > 0 {
		abs, err := resolveWithinAllow(dir, t.AllowDirs)
		if err != nil {
			return "", fmt.Errorf("opencode: bad dir: %w", err)
		}
		dir = abs
	}
	bin := t.bin()
	if bin == "" {
		return "opencode unavailable: no `opencode` binary on PATH and OPENCODE_BIN unset. " +
			"Proceed by pulling the files instead: use list/search to locate them, read to inspect, " +
			"and write/bash to fix. Install OpenCode from https://opencode.ai to enable delegation.", nil
	}
	sc := &llm.Sidecar{Bin: bin, Dir: dir, Model: t.Model, Timeout: t.timeout()}
	out, err := sc.Run(ctx, task, nil)
	if err != nil {
		return "", fmt.Errorf("opencode delegation failed (%v) — fall back to pulling the files and fixing directly", err)
	}
	out = strings.TrimSpace(out)
	if len(out) > 30*1024 {
		out = out[:30*1024] + "\n[…truncated 30KB…]"
	}
	if out == "" {
		return "opencode completed with no text output — verify the working tree yourself (list/search/read) before reporting.", nil
	}
	return out, nil
}
