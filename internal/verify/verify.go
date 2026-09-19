// Package verify runs fast, file-scoped correctness checkers after an
// edit/write/patch succeeds, folding diagnostics into the tool result
// (the OpenCode edit→LSP feedback shape, without bundling language
// servers). Every checker degrades to "skipped: reason" — a missing
// binary never fails an edit.
package verify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// timeout bounds each checker.
const timeout = 30 * time.Second

// Result is one checker's outcome. Skipped is informational, not failure.
type Result struct {
	Checker string
	OK      bool
	Skipped string // non-empty when the checker couldn't run
	Details string // diagnostics on failure
}

// Check selects checkers by file extension and runs them.
func Check(path string) []Result {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return []Result{gofmtCheck(path), goVetFile(path)}
	case ".py":
		return []Result{pyCompile(path)}
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return []Result{nodeCheck(path)}
	case ".sh", ".bash":
		return []Result{shCheck(path)}
	}
	return nil
}

// Report renders results for tool-result injection. Empty when nothing ran.
func Report(rs []Result) string {
	var b strings.Builder
	for _, r := range rs {
		switch {
		case r.Skipped != "":
			fmt.Fprintf(&b, "\n[verify %s skipped: %s]", r.Checker, r.Skipped)
		case r.OK:
			fmt.Fprintf(&b, "\n[verify %s: clean]", r.Checker)
		default:
			fmt.Fprintf(&b, "\n[verify %s FAILED]\n%s", r.Checker, r.Details)
		}
	}
	return b.String()
}

func runTool(ctx context.Context, dir, bin string, args ...string) (string, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("%s not installed", bin)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(stdout.String() + stderr.String())
		if out == "" {
			out = err.Error()
		}
		return "", fmt.Errorf("%s", out)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func withTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

// gofmtCheck parses the file (gofmt -e). It reports syntax errors and
// formatting drift without touching the file.
func gofmtCheck(path string) Result {
	ctx, cancel := withTimeout()
	defer cancel()
	out, err := runTool(ctx, "", "gofmt", "-e", "-l", path)
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return Result{Checker: "gofmt", Skipped: "gofmt not installed"}
		}
		return Result{Checker: "gofmt", Details: capLines(err.Error(), 20)}
	}
	if strings.TrimSpace(out) != "" {
		return Result{Checker: "gofmt", Details: "file needs gofmt (run gofmt -w " + path + ")"}
	}
	return Result{Checker: "gofmt", OK: true}
}

// goVetFile type-checks a single file via `go vet` on a temp package shim?
// No — vet needs package context. Instead compile-check the file's syntax
// tree depth via `go/parser`? Simpler honest scope: gofmt -e already parses.
// So goVetFile runs `go vet` on the file's directory package when the file
// sits inside a module, else skips. Package-level failures reference the
// file only when the diagnostic mentions it.
func goVetFile(path string) Result {
	ctx, cancel := withTimeout()
	defer cancel()
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	out, err := runTool(ctx, dir, "go", "vet", ".")
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return Result{Checker: "go vet", Skipped: "go toolchain not installed"}
		}
		msg := err.Error()
		if strings.Contains(msg, "go.mod") || strings.Contains(msg, "outside module") {
			return Result{Checker: "go vet", Skipped: "file outside a Go module"}
		}
		if !strings.Contains(msg, base) {
			// Package has errors elsewhere — informative, not this edit's fault.
			return Result{Checker: "go vet", Details: "(package has pre-existing errors unrelated to " + base + ")\n" + capLines(msg, 10)}
		}
		return Result{Checker: "go vet", Details: capLines(msg, 20)}
	}
	_ = out
	return Result{Checker: "go vet", OK: true}
}

// pyCompile byte-compiles the file.
func pyCompile(path string) Result {
	ctx, cancel := withTimeout()
	defer cancel()
	_, err := runTool(ctx, "", "python3", "-m", "py_compile", path)
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return Result{Checker: "py_compile", Skipped: "python3 not installed"}
		}
		return Result{Checker: "py_compile", Details: capLines(err.Error(), 20)}
	}
	return Result{Checker: "py_compile", OK: true}
}

// nodeCheck syntax-checks JS/TS via node --check (types are not checked;
// that needs tsc and project context).
func nodeCheck(path string) Result {
	ctx, cancel := withTimeout()
	defer cancel()
	_, err := runTool(ctx, "", "node", "--check", path)
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return Result{Checker: "node --check", Skipped: "node not installed"}
		}
		return Result{Checker: "node --check", Details: capLines(err.Error(), 20)}
	}
	return Result{Checker: "node --check", OK: true}
}

// shCheck parses shell scripts with sh -n.
func shCheck(path string) Result {
	ctx, cancel := withTimeout()
	defer cancel()
	_, err := runTool(ctx, "", "sh", "-n", path)
	if err != nil {
		return Result{Checker: "sh -n", Details: capLines(err.Error(), 20)}
	}
	return Result{Checker: "sh -n", OK: true}
}

func capLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("...[%d more lines]", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}
