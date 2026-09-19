package tools

import (
	"context"
	"testing"
)

func TestGitToolValidation(t *testing.T) {
	g := &GitTool{Workdir: t.TempDir()}
	if _, err := g.Execute(context.Background(), map[string]any{"action": "frobnicate"}); err == nil {
		t.Fatal("unknown action must error")
	}
	// Temp dir is not a repo: status must fail gracefully (with or without git).
	if _, err := g.Execute(context.Background(), map[string]any{"action": "status"}); err == nil {
		t.Fatal("status outside a repo must error")
	}
	// Scope escape rejected before git runs.
	if _, err := g.Execute(context.Background(), map[string]any{"action": "log", "path": "../evil"}); err == nil {
		t.Fatal("scope escape must be rejected")
	}
}
