// Package vcs gives the agent structured git awareness: repository
// detection plus status/diff/log as parsed data instead of raw shell
// output. All commands run read-only (no checkout/commit/stash here);
// each degrades to a clear "not a git repo / git unavailable" message.
package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// timeout bounds every git invocation.
const timeout = 20 * time.Second

// Info describes a workdir's VCS state for prompt injection.
type Info struct {
	IsRepo bool
	Root   string
	Branch string
	Clean  bool
}

// Detect reports whether dir is inside a git work tree.
func Detect(dir string) Info {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	root, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}
	}
	branch, _ := run(ctx, dir, "branch", "--show-current")
	status, _ := run(ctx, dir, "status", "--porcelain")
	return Info{IsRepo: true, Root: root, Branch: branch, Clean: strings.TrimSpace(status) == ""}
}

// Status returns porcelain status lines (empty = clean).
func Status(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return run(ctx, dir, "status", "--porcelain")
}

// Diff returns the unified diff of tracked modifications (capped).
func Diff(dir string, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := run(ctx, dir, "diff", "--stat", "--", ".")
	if err != nil {
		return "", err
	}
	patch, err := run(ctx, dir, "diff", "--", ".")
	if err != nil {
		return out, nil
	}
	if limit > 0 && len(patch) > limit {
		patch = patch[:limit] + fmt.Sprintf("\n...[diff truncated %d bytes]", len(patch)-limit)
	}
	if strings.TrimSpace(out) == "" {
		return "(clean)", nil
	}
	return out + "\n" + patch, nil
}

// Log returns one-line history for path (default ".").
func Log(dir, path string, n int) (string, error) {
	if n <= 0 || n > 50 {
		n = 10
	}
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return run(ctx, dir, "log", fmt.Sprintf("--max-count=%d", n),
		"--pretty=format:%h %ad %an %s", "--date=short", "--", path)
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git unavailable: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// Normalize the common non-repo case into plain words.
		if strings.Contains(msg, "not a git repository") {
			return "", fmt.Errorf("not a git repository")
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
