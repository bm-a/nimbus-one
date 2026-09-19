package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Tool limits.
const (
	maxReadBytes  = 100 * 1024
	maxOutputLine = 2000
)

// toolResult is what the model sees back.
type toolResult struct {
	output string
}

// toolRead reads a file inside the workspace (capped at 100KB).
func toolRead(path string) (string, error) {
	abs, err := resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("read %q: file does not exist in the workspace", path)
		}
		return "", fmt.Errorf("read %q: %v", path, err)
	}
	if len(data) > maxReadBytes {
		return "", fmt.Errorf("read %q: file is %d bytes (limit %d) — ask me to read a smaller section", path, len(data), maxReadBytes)
	}
	return string(data), nil
}

// toolWrite creates or overwrites a file inside the workspace.
// Parent directories are created. Never touches files outside the jail.
func toolWrite(path, content string) (string, error) {
	abs, err := resolve(path)
	if err != nil {
		return "", err
	}
	if dir := filepath.Dir(abs); dir != workspaceRoot {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("write %q: cannot create folders: %v", path, err)
		}
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %v", path, err)
	}
	rel, _ := filepath.Rel(workspaceRoot, abs)
	return fmt.Sprintf("wrote %d bytes to %s", len(content), rel), nil
}

// toolEdit applies one search/replace edit. The search text must occur
// exactly once; zero or multiple matches are errors that show a snippet,
// so the model can narrow its match instead of corrupting the file.
func toolEdit(path, oldText, newText string) (string, error) {
	if oldText == "" {
		return "", fmt.Errorf("edit: search text must not be empty")
	}
	abs, err := resolve(path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("edit %q: file does not exist in the workspace", path)
		}
		return "", fmt.Errorf("edit %q: %v", path, err)
	}
	body := string(raw)
	switch n := strings.Count(body, oldText); {
	case n == 0:
		return "", fmt.Errorf("edit %q: text not found (must match exactly once):\n--- file starts ---\n%s", path, snippet(body))
	case n > 1:
		return "", fmt.Errorf("edit %q: text occurs %d times, must be unique — include more surrounding lines:\n--- file starts ---\n%s", path, n, snippet(body))
	}
	updated := strings.Replace(body, oldText, newText, 1)
	if updated == body {
		return fmt.Sprintf("edit %s: no change (new text identical)", path), nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("edit %q: %v", path, err)
	}
	if err := os.WriteFile(abs, []byte(updated), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("edit %q: %v", path, err)
	}
	return fmt.Sprintf("edit %s: 1 change applied", path), nil
}

// toolList lists a directory inside the workspace.
func toolList(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := resolve(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("list %q: %v", path, err)
	}
	if len(entries) == 0 {
		return "(empty folder)", nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > maxOutputLine {
		names = append(names[:maxOutputLine], fmt.Sprintf("...[%d total entries]", len(entries)))
	}
	return strings.Join(names, "\n"), nil
}

// toolShell runs a command with the working directory locked to the
// workspace root. The command text must first pass checkShellText, and
// the caller must obtain typed-yes confirmation (see confirm.go) before
// calling this — this function never prompts by itself.
func toolShell(command string, timeoutSec int) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("shell: empty command")
	}
	if err := checkShellText(command); err != nil {
		return "", err
	}
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspaceRoot // locked: relative access stays inside
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if stderr.Len() > 0 {
		out += "\n[stderr]\n" + stderr.String()
	}
	if len(out) > maxReadBytes {
		out = out[:maxReadBytes] + fmt.Sprintf("\n...[output cut at %d bytes]", maxReadBytes)
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("shell: timed out after %d seconds", timeoutSec)
	}
	if err != nil {
		return out, fmt.Errorf("shell: command failed: %v", err)
	}
	return out, nil
}

// snippet returns the first 15 numbered lines for mismatch errors.
func snippet(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 15 {
		lines = lines[:15]
	}
	var b strings.Builder
	for i, ln := range lines {
		if len(ln) > 160 {
			ln = ln[:160] + "…"
		}
		fmt.Fprintf(&b, "%4d| %s\n", i+1, ln)
	}
	return strings.TrimRight(b.String(), "\n")
}
