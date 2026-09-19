// Structured git awareness tool (read-only).
//
// Status/diff/log as parsed data instead of raw shell output so the model
// reasons over structure. Never mutates: no checkout/commit/stash paths
// exist here by design.
package tools

import (
	"context"
	"fmt"
	"strings"

	"nimbus-one/internal/vcs"
)

// GitTool exposes read-only git inspection scoped to the workspace.
type GitTool struct {
	Workdir string
}

// Name returns "git".
func (t *GitTool) Name() string { return "git" }

// Description describes the git tool.
func (t *GitTool) Description() string {
	return "Read-only git inspection (status/diff/log). Args: action (required: status|diff|log), path (file/dir for log/diff scope, default workspace), n (log entries, default 10). Never mutates the repo."
}

// Parameters describes the git arguments.
func (t *GitTool) Parameters() map[string]Param {
	return map[string]Param{
		"action": {Type: "string", Description: "status|diff|log", Required: true},
		"path":   {Type: "string", Description: "Scope path (log/diff), relative to workspace"},
		"n":      {Type: "number", Description: "Log entries (default 10, max 50)"},
	}
}

// Execute runs the git action.
func (t *GitTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	action := strings.ToLower(strings.TrimSpace(stringArg(args, "action")))
	dir := t.Workdir
	if dir == "" {
		dir = "."
	}
	// An explicit scope stays inside the workspace like every fs path.
	scope := strings.TrimSpace(stringArg(args, "path"))
	if scope == "" {
		scope = "."
	}
	if t.Workdir != "" {
		abs, err := resolveWithinAllow(scope, []string{t.Workdir})
		if err != nil {
			return "", fmt.Errorf("git: %w", err)
		}
		scope = abs
	}
	switch action {
	case "status":
		out, err := vcs.Status(dir)
		if err != nil {
			return "", fmt.Errorf("git status: %w", err)
		}
		if strings.TrimSpace(out) == "" {
			return "git status: clean", nil
		}
		return "git status:\n" + out, nil
	case "diff":
		out, err := vcs.Diff(dir, 20*1024)
		if err != nil {
			return "", fmt.Errorf("git diff: %w", err)
		}
		return out, nil
	case "log":
		n := 10
		if v, ok := numberArg(args, "n"); ok {
			n = int(v)
		}
		out, err := vcs.Log(dir, scope, n)
		if err != nil {
			return "", fmt.Errorf("git log: %w", err)
		}
		if strings.TrimSpace(out) == "" {
			return "git log: no commits", nil
		}
		return out, nil
	}
	return "", fmt.Errorf("git: unknown action %q (want status|diff|log)", action)
}
