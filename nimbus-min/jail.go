// Package main implements Nimbus-One: a minimal Anthropic-only agent
// runtime bound to exactly one workspace directory.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workspaceRoot is the absolute, symlink-resolved workspace directory.
// Set once at startup; every file and shell operation is confined to it.
var workspaceRoot string

// initJail resolves and locks the workspace root. It must be called once
// before any tool runs.
func initJail(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("workspace %q: %v", root, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("workspace %q: %v", root, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return fmt.Errorf("workspace %q: %v", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace %q is not a directory", root)
	}
	workspaceRoot = real
	return nil
}

// resolve maps a user/model-supplied path into the workspace.
// Policy (all enforced, all tested):
//   - empty paths and NUL bytes are rejected
//   - absolute paths are rejected (use workspace-relative paths)
//   - ".." escapes are rejected after cleaning
//   - symlinks are resolved and the result re-checked (no symlink escape)
func resolve(p string) (string, error) {
	if workspaceRoot == "" {
		return "", fmt.Errorf("workspace not initialized")
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("invalid path: NUL byte")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path must not be empty (use \".\" for the workspace root)")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute paths are not allowed — use a path inside the workspace, e.g. notes/todo.md")
	}
	joined := filepath.Join(workspaceRoot, filepath.Clean(p))
	real, err := resolveSymlinks(joined)
	if err != nil {
		return "", err
	}
	if real != workspaceRoot && !strings.HasPrefix(real, workspaceRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the workspace", p)
	}
	return real, nil
}

// resolveSymlinks resolves symlinks for existing paths, or for the
// longest existing ancestor plus the unresolved remainder. This closes
// the symlink-escape hole for both reads (target exists) and writes
// (target does not exist yet but a parent is a symlink outward).
func resolveSymlinks(p string) (string, error) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real, nil
	}
	// Walk up to the longest existing ancestor, collecting the
	// unresolved remainder along the way.
	rest := []string{}
	cur := p
	for {
		rest = append(rest, filepath.Base(cur))
		cur = filepath.Dir(cur)
		if cur == filepath.Dir(cur) {
			return "", fmt.Errorf("path %q escapes the workspace", p)
		}
		if _, err := os.Lstat(cur); err == nil {
			realParent, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", fmt.Errorf("path %q: %v", p, err)
			}
			out := realParent
			for i := len(rest) - 1; i >= 0; i-- {
				out = filepath.Join(out, rest[i])
			}
			return out, nil
		}
	}
}

// checkShellText rejects shell commands that attempt to leave the
// workspace. The shell runs with its working directory locked to the
// workspace root, so relative access stays inside; this check closes the
// remaining holes: absolute paths, ".." segments, and "cd /..." escapes.
// Tokens that are clearly not paths (URLs, flags without path values)
// are left alone; anything ambiguous is rejected with guidance.
func checkShellText(cmd string) error {
	if strings.ContainsRune(cmd, 0) {
		return fmt.Errorf("invalid command: NUL byte")
	}
	for _, tok := range splitShellTokens(cmd) {
		t := strings.Trim(tok, `'"`)
		if t == "" {
			continue
		}
		// Strip flag prefixes: --output=/tmp/x must still be caught.
		if strings.HasPrefix(t, "--") {
			if i := strings.Index(t, "="); i >= 0 {
				t = t[i+1:]
				t = strings.Trim(t, `'"`)
			} else {
				continue
			}
		} else if strings.HasPrefix(t, "-") && !strings.Contains(t, "/") {
			continue // short flags like -rf carry no path
		}
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			continue
		}
		if strings.HasPrefix(t, "/") {
			return fmt.Errorf("command references absolute path %q — use workspace-relative paths", t)
		}
		if t == ".." || strings.HasPrefix(t, "../") || strings.Contains(t, "/../") || strings.HasSuffix(t, "/..") {
			return fmt.Errorf("command escapes the workspace with %q — stay inside the workspace", t)
		}
	}
	return nil
}

// splitShellTokens splits on whitespace, quote boundaries, and shell
// operators. This is a conservative safety scan, not a shell parser:
// splitting quoted spans into pieces only adds more substrings to check,
// and every piece is still examined, so quoting can never hide a path.
func splitShellTokens(s string) []string {
	isSep := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '|', '&', '<', '>', '(', ')', '=', '\'', '"', '`', '$':
			return true
		}
		return false
	}
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if isSep(r) {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return toks
}
