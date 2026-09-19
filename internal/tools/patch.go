// Multi-file patch tool (apply-patch envelope).
//
// Applies Add/Update/Delete operations across files in one atomic-feeling
// call: every operation validates first, then all apply. Update blocks are
// exact-match (context lines + removed lines form the old block, added
// lines the new block); the old block must occur exactly once.
//
// Envelope:
//
//	*** Begin Patch
//	*** Add File: rel/path
//	+line
//	*** Update File: rel/path
//	  context line
//	-removed line
//	+added line
//	*** Delete File: rel/path
//	*** End Patch
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nimbus-one/internal/verify"
)

// PatchTool applies multi-file patches.
type PatchTool struct {
	AllowDirs []string
}

// Name returns "apply_patch".
func (t *PatchTool) Name() string { return "apply_patch" }

// Description describes the patch tool.
func (t *PatchTool) Description() string {
	return "Apply a multi-file patch envelope (Add/Update/Delete). Args: patch (required envelope text). Update blocks match exactly and must be unique. Prefer this over edit for GPT-family models and multi-file changes."
}

// Parameters describes the patch arguments.
func (t *PatchTool) Parameters() map[string]Param {
	return map[string]Param{
		"patch": {Type: "string", Description: "Patch envelope text", Required: true},
	}
}

// patchOp is one parsed operation.
type patchOp struct {
	kind string // add | update | delete
	path string
	body []string
}

// Execute parses, validates, then applies the patch.
func (t *PatchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	text := stringArg(args, "patch")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("apply_patch: empty patch")
	}
	ops, err := parsePatch(text)
	if err != nil {
		return "", err
	}
	// Resolve + policy-check every path before touching anything.
	type prepared struct {
		op  patchOp
		abs string
	}
	prep := make([]prepared, 0, len(ops))
	for _, op := range ops {
		if err := Policy.CheckEdit(op.path); err != nil {
			return "", fmt.Errorf("apply_patch: %w", err)
		}
		abs, err := resolveWithinAllow(op.path, t.AllowDirs)
		if err != nil {
			return "", fmt.Errorf("apply_patch: %w", err)
		}
		prep = append(prep, prepared{op, abs})
	}
	var receipts []string
	for _, p := range prep {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		r, err := sharedMutations.Run(p.abs, func() (string, error) {
			return applyPatchOp(p.op, p.abs)
		})
		if err != nil {
			return "", fmt.Errorf("apply_patch %s: %w", p.op.path, err)
		}
		receipts = append(receipts, r)
	}
	return fmt.Sprintf("apply_patch: %d operation(s)\n%s", len(receipts), strings.Join(receipts, "\n")), nil
}

func parsePatch(text string) ([]patchOp, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	// Trim leading blanks; require the Begin marker.
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "*** Begin Patch" {
		return nil, fmt.Errorf("apply_patch: envelope must start with *** Begin Patch")
	}
	i++
	var ops []patchOp
	var cur *patchOp
	flush := func() {
		if cur != nil {
			ops = append(ops, *cur)
			cur = nil
		}
	}
	for ; i < len(lines); i++ {
		ln := lines[i]
		t := strings.TrimSpace(ln)
		switch {
		case t == "*** End Patch":
			flush()
			// Trailing blanks allowed; anything else is an error.
			for _, rest := range lines[i+1:] {
				if strings.TrimSpace(rest) != "" {
					return nil, fmt.Errorf("apply_patch: content after *** End Patch")
				}
			}
			if len(ops) == 0 {
				return nil, fmt.Errorf("apply_patch: no operations")
			}
			return ops, nil
		case strings.HasPrefix(t, "*** Add File:"):
			flush()
			cur = &patchOp{kind: "add", path: strings.TrimSpace(strings.TrimPrefix(t, "*** Add File:"))}
		case strings.HasPrefix(t, "*** Update File:"):
			flush()
			cur = &patchOp{kind: "update", path: strings.TrimSpace(strings.TrimPrefix(t, "*** Update File:"))}
		case strings.HasPrefix(t, "*** Delete File:"):
			flush()
			cur = &patchOp{kind: "delete", path: strings.TrimSpace(strings.TrimPrefix(t, "*** Delete File:"))}
		case strings.HasPrefix(t, "***"):
			return nil, fmt.Errorf("apply_patch: unknown marker %q", t)
		default:
			if cur == nil {
				if t == "" {
					continue
				}
				return nil, fmt.Errorf("apply_patch: line outside any operation: %q", truncatePreview(t, 80))
			}
			cur.body = append(cur.body, ln)
		}
	}
	return nil, fmt.Errorf("apply_patch: missing *** End Patch")
}

func applyPatchOp(op patchOp, abs string) (string, error) {
	if strings.TrimSpace(op.path) == "" {
		return "", fmt.Errorf("empty file path")
	}
	switch op.kind {
	case "delete":
		if err := os.Remove(abs); err != nil {
			return "", fmt.Errorf("delete %s: %w", abs, err)
		}
		return fmt.Sprintf("delete %s", abs), nil
	case "add":
		var b strings.Builder
		for _, ln := range op.body {
			if !strings.HasPrefix(ln, "+") {
				return "", fmt.Errorf("add %s: every line must start with '+': %q", abs, truncatePreview(ln, 80))
			}
			b.WriteString(strings.TrimPrefix(ln, "+"))
			b.WriteByte('\n')
		}
		if _, err := os.Stat(abs); err == nil {
			return "", fmt.Errorf("add %s: file already exists (use update)", abs)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", fmt.Errorf("add %s: mkdir: %w", abs, err)
		}
		if err := os.WriteFile(abs, []byte(b.String()), 0o644); err != nil {
			return "", fmt.Errorf("add %s: %w", abs, err)
		}
		return fmt.Sprintf("add %s (%d lines)%s", abs, strings.Count(b.String(), "\n"), verify.Report(verify.Check(abs))), nil
	case "update":
		var oldB, newB strings.Builder
		for _, ln := range op.body {
			switch {
			case strings.HasPrefix(ln, " "):
				oldB.WriteString(strings.TrimPrefix(ln, " "))
				oldB.WriteByte('\n')
				newB.WriteString(strings.TrimPrefix(ln, " "))
				newB.WriteByte('\n')
			case strings.HasPrefix(ln, "-"):
				oldB.WriteString(strings.TrimPrefix(ln, "-"))
				oldB.WriteByte('\n')
			case strings.HasPrefix(ln, "+"):
				newB.WriteString(strings.TrimPrefix(ln, "+"))
				newB.WriteByte('\n')
			case strings.TrimSpace(ln) == "":
				oldB.WriteByte('\n')
				newB.WriteByte('\n')
			default:
				return "", fmt.Errorf("update %s: lines must start with ' ', '-', or '+': %q", abs, truncatePreview(ln, 80))
			}
		}
		oldBlock, newBlock := oldB.String(), newB.String()
		if oldBlock == "" {
			return "", fmt.Errorf("update %s: empty old block", abs)
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("update %s: read: %w", abs, err)
		}
		body := string(raw)
		switch n := strings.Count(body, oldBlock); {
		case n == 0:
			return "", fmt.Errorf("update %s: old block not found (exact match required)", abs)
		case n > 1:
			return "", fmt.Errorf("update %s: old block occurs %d times, must be unique (add context lines)", abs, n)
		}
		updated := strings.Replace(body, oldBlock, newBlock, 1)
		st, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("update %s: stat: %w", abs, err)
		}
		if err := os.WriteFile(abs, []byte(updated), st.Mode().Perm()); err != nil {
			return "", fmt.Errorf("update %s: write: %w", abs, err)
		}
		return fmt.Sprintf("update %s%s", abs, verify.Report(verify.Check(abs))), nil
	}
	return "", fmt.Errorf("unknown op %q", op.kind)
}
