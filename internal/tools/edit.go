// Exact-match file editing tool.
//
// OpenClaw reference (read-only):
//
//	sessions/tools/edit.ts (oldText/newText exact matching, mismatch errors),
//	sessions/tools/path-utils.ts (workspace scoping).
package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// bomPrefix is preserved verbatim when present.
var bomPrefix = []byte{0xEF, 0xBB, 0xBF}

// EditOp is a single exact-match replacement.
type EditOp struct {
	OldText string
	NewText string
}

// EditTool applies exact-match replacements to a file.
type EditTool struct {
	AllowDirs []string
}

// Name returns "edit".
func (t *EditTool) Name() string { return "edit" }

// Description describes the edit tool.
func (t *EditTool) Description() string {
	return "Edit a file with exact-match replacements (BOM/line-endings preserved). Args: path (required), edits (required array of {oldText, newText}). Every oldText must occur exactly once; mismatches error with a file snippet. No-op edits return a receipt without touching the file."
}

// Parameters describes the edit arguments.
func (t *EditTool) Parameters() map[string]Param {
	return map[string]Param{
		"path":  {Type: "string", Description: "File path to edit", Required: true},
		"edits": {Type: "object", Description: "Array of {oldText, newText} replacements", Required: true},
	}
}

// Execute applies the edits atomically (serialized per path).
func (t *EditTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := Policy.CheckEdit(stringArg(args, "path")); err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	abs, err := resolveWithinAllow(stringArg(args, "path"), t.AllowDirs)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	ops, err := parseEditOps(args["edits"])
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	return sharedMutations.Run(abs, func() (string, error) {
		return applyEdits(ctx, abs, ops)
	})
}

func parseEditOps(v any) ([]EditOp, error) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("missing or empty edits array (need [{oldText, newText}, ...])")
	}
	ops := make([]EditOp, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edits[%d]: must be an object", i)
		}
		oldText, _ := m["oldText"].(string)
		if oldText == "" {
			// Tolerate stringified values via fmt like other tools.
			if s, ok := m["oldText"]; ok && s != nil {
				oldText = fmt.Sprint(s)
			}
		}
		if oldText == "" {
			return nil, fmt.Errorf("edits[%d]: missing oldText", i)
		}
		newText := ""
		if s, ok := m["newText"]; ok && s != nil {
			if str, ok := s.(string); ok {
				newText = str
			} else {
				newText = fmt.Sprint(s)
			}
		}
		ops = append(ops, EditOp{OldText: oldText, NewText: newText})
	}
	return ops, nil
}

func applyEdits(ctx context.Context, abs string, ops []EditOp) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", abs, err)
	}
	body := string(raw)
	hasBOM := false
	if strings.HasPrefix(body, string(bomPrefix)) {
		hasBOM = true
		body = strings.TrimPrefix(body, string(bomPrefix))
	}
	// NOTE: no line-ending normalization — matching operates on raw bytes
	// so CRLF/LF files keep their style automatically.

	// Idempotent fast path: every op is already a no-op.
	allNoop := true
	for _, op := range ops {
		if op.OldText != op.NewText {
			allNoop = false
			break
		}
	}
	if allNoop {
		return fmt.Sprintf("edit %s: no-op receipt (0 changes, %d edit(s) already applied)", abs, len(ops)), nil
	}

	updated := body
	var previews []string
	for i, op := range ops {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count := strings.Count(updated, op.OldText)
		switch {
		case count == 0:
			return "", fmt.Errorf("edits[%d]: oldText not found (exact match required):\n--- file snippet (%s, %d lines) ---\n%s",
				i, abs, countLines(body), headSnippet(body, 15))
		case count > 1:
			return "", fmt.Errorf("edits[%d]: oldText occurs %d times, must be unique (narrow the match with more context):\n--- file snippet (%s) ---\n%s",
				i, count, abs, headSnippet(body, 15))
		}
		updated = strings.Replace(updated, op.OldText, op.NewText, 1)
		previews = append(previews, fmt.Sprintf("--- edit %d ---\n- %s\n+ %s", i+1,
			truncatePreview(op.OldText, 240), truncatePreview(op.NewText, 240)))
	}

	if updated == body {
		return fmt.Sprintf("edit %s: no-op receipt (0 changes, content already up to date)", abs), nil
	}
	out := updated
	if hasBOM {
		out = string(bomPrefix) + out
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", abs, err)
	}
	if err := os.WriteFile(abs, []byte(out), st.Mode().Perm()); err != nil {
		return "", fmt.Errorf("write %s: %w", abs, err)
	}
	return fmt.Sprintf("edit %s: applied %d change(s)\n%s", abs, len(ops), strings.Join(previews, "\n")), nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// headSnippet returns the first n lines numbered, for mismatch errors.
func headSnippet(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	var b strings.Builder
	for i, ln := range lines {
		t := ln
		if len(t) > 160 {
			t = t[:160] + "…"
		}
		fmt.Fprintf(&b, "%4d| %s\n", i+1, t)
	}
	if countLines(s) > n {
		fmt.Fprintf(&b, "...[%d total lines]", countLines(s))
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncatePreview(s string, limit int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
