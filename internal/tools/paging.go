// Line paging and .gitignore-aware search filtering.
//
// OpenClaw reference (read-only):
//
//	sessions/tools/read.ts (offset/limit cursors, truncation spill),
//	sessions/tools/ls.ts + find.ts + grep.ts (fd/rg listing, gitignore
//	pruning, result cursors). Nimbus port: stdlib LinePager + Ignore matcher,
//	wired into SearchTool (see fs.go) without touching its public shape.
package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPageLines bounds a single paged read.
const DefaultPageLines = 200

// LinePager reads files by line cursors.
type LinePager struct {
	AllowDirs []string
}

// Read returns lines[offsetLines:offsetLines+limitLines], the next cursor,
// and whether more lines remain. nextCursor is -1 when EOF is reached.
func (p *LinePager) Read(path string, offsetLines, limitLines int) ([]string, int, error) {
	abs, err := resolveWithinAllow(path, p.AllowDirs)
	if err != nil {
		return nil, 0, err
	}
	if offsetLines < 0 {
		offsetLines = 0
	}
	if limitLines <= 0 || limitLines > 2000 {
		limitLines = DefaultPageLines
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if st.IsDir() {
		return nil, 0, fmt.Errorf("paged read: %s is a directory", abs)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var lines []string
	idx := 0
	for sc.Scan() {
		if idx >= offsetLines && len(lines) < limitLines {
			lines = append(lines, sc.Text())
		}
		idx++
		if idx >= offsetLines+limitLines {
			// Peek whether more lines remain.
			if sc.Scan() {
				return lines, offsetLines + len(lines), nil
			}
			return lines, -1, sc.Err()
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}
	if idx <= offsetLines {
		return nil, -1, nil
	}
	return lines, -1, nil
}

// PagedReadTool exposes line-cursor reads to the agent loop.
type PagedReadTool struct {
	AllowDirs []string
}

// Name returns "read_paged".
func (t *PagedReadTool) Name() string { return "read_paged" }

// Description describes the paged read tool.
func (t *PagedReadTool) Description() string {
	return "Read a file by line numbers (for large files). Args: path (required), offset_lines (0-based, default 0), limit_lines (default 200, max 2000). Returns numbered lines plus next_cursor (-1 at EOF)."
}

// Parameters describes the paged read arguments.
func (t *PagedReadTool) Parameters() map[string]Param {
	return map[string]Param{
		"path":         {Type: "string", Description: "File path to read", Required: true},
		"offset_lines": {Type: "number", Description: "0-based first line"},
		"limit_lines":  {Type: "number", Description: "Max lines (default 200)"},
	}
}

// Execute reads the requested line window.
func (t *PagedReadTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	offset := 0
	if v, ok := numberArg(args, "offset_lines"); ok && v > 0 {
		offset = int(v)
	}
	limit := DefaultPageLines
	if v, ok := numberArg(args, "limit_lines"); ok && v > 0 {
		limit = int(v)
	}
	p := &LinePager{AllowDirs: t.AllowDirs}
	lines, next, err := p.Read(stringArg(args, "path"), offset, limit)
	if err != nil {
		return "", fmt.Errorf("read_paged: %w", err)
	}
	var b strings.Builder
	for i, ln := range lines {
		t := ln
		if len(t) > 500 {
			t = t[:500] + "…"
		}
		fmt.Fprintf(&b, "%6d| %s\n", offset+i+1, t)
	}
	fmt.Fprintf(&b, "[next_cursor=%d]", next)
	return b.String(), nil
}

// --- .gitignore matching ---

type ignoreRule struct {
	pattern string
	dirOnly bool
	negate  bool
}

// Ignore holds compiled .gitignore rules for one root directory.
type Ignore struct {
	root  string
	rules []ignoreRule
}

// LoadDir reads <dir>/.gitignore (missing file → empty matcher, nil error).
func LoadDir(dir string) (*Ignore, error) {
	ig := &Ignore{root: dir}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return ig, nil
		}
		return nil, err
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(ln, "!") {
			r.negate = true
			ln = strings.TrimSpace(strings.TrimPrefix(ln, "!"))
		}
		if strings.HasSuffix(ln, "/") {
			r.dirOnly = true
			ln = strings.TrimSuffix(ln, "/")
		}
		ln = strings.TrimPrefix(ln, "/")
		if ln == "" {
			continue
		}
		r.pattern = ln
		ig.rules = append(ig.rules, r)
	}
	return ig, nil
}

// Matched reports whether relOrAbs (file or dir path) is ignored.
// Later rules override earlier ones; negation re-includes.
func (ig *Ignore) Matched(relOrAbs string) bool {
	if ig == nil || len(ig.rules) == 0 {
		return false
	}
	rel := relOrAbs
	if filepath.IsAbs(relOrAbs) && ig.root != "" {
		if r, err := filepath.Rel(ig.root, relOrAbs); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	rel = filepath.ToSlash(rel)
	ignored := false
	for _, r := range ig.rules {
		if r.match(rel) {
			if r.negate {
				ignored = false
			} else {
				ignored = true
			}
		}
	}
	return ignored
}

func (r ignoreRule) match(rel string) bool {
	pat := r.pattern
	if strings.Contains(pat, "/") {
		// Anchored (or subtree) pattern: match against full relative path,
		// with "**" simplified to "*" (documented limitation).
		p := strings.ReplaceAll(pat, "**", "*")
		if ok, _ := filepath.Match(p, rel); ok {
			return true
		}
		// Directory-prefix rule (e.g. "build" from "build/") also ignores
		// everything underneath.
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
		if !strings.Contains(p, "*") {
			return false
		}
		// Glob over separators: try segment-wise suffix match.
		segPat := strings.Split(p, "/")
		segRel := strings.Split(rel, "/")
		if len(segRel) < len(segPat) {
			return false
		}
		for i := range segPat {
			ok, _ := filepath.Match(segPat[i], segRel[len(segRel)-len(segPat)+i])
			if !ok {
				return false
			}
		}
		return true
	}
	// Bare pattern: match any single path segment (gitignore semantics).
	for _, seg := range strings.Split(rel, "/") {
		if ok, _ := filepath.Match(pat, seg); ok {
			return true
		}
	}
	return false
}
