package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ReadLimit caps bytes returned by ReadTool.
const ReadLimit = 100 * 1024

// SearchCap caps results returned by SearchTool.
const SearchCap = 50

// errSearchCap stops the walk once the result cap is reached.
var errSearchCap = errors.New("search: result cap reached")

// resolveWithinAllow cleans p and enforces containment in allow when set.
// Relative paths resolve against allow[0] (the workspace), so agent-side
// "list ." means the workspace — not whatever directory the binary was
// launched from. With empty allow it requires a non-empty absolute path.
func resolveWithinAllow(p string, allow []string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("invalid path")
	}
	base := ""
	if len(allow) > 0 && !filepath.IsAbs(p) {
		base = allow[0]
	}
	abs, err := filepath.Abs(filepath.Join(base, filepath.Clean(p)))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", p, err)
	}
	if len(allow) == 0 {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("path %s must be absolute", p)
		}
		return abs, nil
	}
	for _, a := range allow {
		aa, err := filepath.Abs(filepath.Clean(a))
		if err != nil {
			continue
		}
		if abs == aa || strings.HasPrefix(abs, aa+string(os.PathSeparator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path %s is outside allowed dirs", p)
}

// ReadTool reads file contents (capped at 100KB).
type ReadTool struct {
	AllowDirs []string
}

// Name returns "read".
func (t *ReadTool) Name() string { return "read" }

// Description describes the read tool.
func (t *ReadTool) Description() string {
	return "Read a file in YOUR workspace (max 100KB, always allowed there). Args: path (required, relative paths resolve inside the workspace), offset (bytes, default 0), limit (bytes, default 102400). Never claim inability without calling first."
}

// Parameters describes the read arguments.
func (t *ReadTool) Parameters() map[string]Param {
	return map[string]Param{
		"path":   {Type: "string", Description: "File path to read", Required: true},
		"offset": {Type: "number", Description: "Byte offset to start at"},
		"limit":  {Type: "number", Description: "Max bytes to return (capped at 102400)"},
	}
}

// Execute reads the file with offset/limit handling.
func (t *ReadTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	abs, err := resolveWithinAllow(stringArg(args, "path"), t.AllowDirs)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	var offset int64
	if v, ok := numberArg(args, "offset"); ok && v > 0 {
		offset = int64(v)
	}
	limit := int64(ReadLimit)
	if v, ok := numberArg(args, "limit"); ok && v > 0 {
		limit = int64(v)
		if limit > int64(ReadLimit) {
			limit = int64(ReadLimit)
		}
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("read: %s is a directory", abs)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return "", fmt.Errorf("read: seek: %w", err)
		}
	}
	buf := make([]byte, limit+1)
	n := 0
	for n < len(buf) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		m, rerr := f.Read(buf[n:])
		n += m
		if rerr != nil {
			if rerr != io.EOF {
				return "", fmt.Errorf("read: %w", rerr)
			}
			break
		}
		if m == 0 {
			break
		}
	}
	if n > int(limit) {
		return string(buf[:limit]) + fmt.Sprintf("\n...[truncated at %d bytes]", limit), nil
	}
	return string(buf[:n]), nil
}

// WriteTool writes file contents, creating parent directories.
type WriteTool struct {
	AllowDirs []string
}

// Name returns "write".
func (t *WriteTool) Name() string { return "write" }

// Description describes the write tool.
func (t *WriteTool) Description() string {
	return "Write content to a file in YOUR workspace (creates parent dirs, always allowed there). Args: path (required), content (required). Never claim inability without calling first."
}

// Parameters describes the write arguments.
func (t *WriteTool) Parameters() map[string]Param {
	return map[string]Param{
		"path":    {Type: "string", Description: "File path to write", Required: true},
		"content": {Type: "string", Description: "Content to write", Required: true},
	}
}

// Execute writes content to path.
func (t *WriteTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if err := Policy.CheckWrite(stringArg(args, "path")); err != nil {
		DefaultAudit.Record("write", stringArg(args, "path"), false, err.Error())
		return "", err
	}
	abs, err := resolveWithinAllow(stringArg(args, "path"), t.AllowDirs)
	if err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	content, ok := args["content"]
	if !ok || content == nil {
		return "", fmt.Errorf("write: missing content")
	}
	var data string
	switch v := content.(type) {
	case string:
		data = v
	default:
		data = fmt.Sprint(v)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if dir := filepath.Dir(abs); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("write: mkdir: %w", err)
		}
	}
	if err := os.WriteFile(abs, []byte(data), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(data), abs), nil
}

// ListTool lists directory entries.
type ListTool struct {
	AllowDirs []string
}

// Name returns "list".
func (t *ListTool) Name() string { return "list" }

// Description describes the list tool.
func (t *ListTool) Description() string {
	return "List YOUR workspace directory (always allowed — use path \".\" for the workspace root). Args: path (required directory, relative paths resolve inside the workspace). Never claim inability without calling first."
}

// Parameters describes the list arguments.
func (t *ListTool) Parameters() map[string]Param {
	return map[string]Param{
		"path": {Type: "string", Description: "Directory path to list", Required: true},
	}
}

// Execute lists the directory.
func (t *ListTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	abs, err := resolveWithinAllow(stringArg(args, "path"), t.AllowDirs)
	if err != nil {
		return "", fmt.Errorf("list: %w", err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("list: %w", err)
	}
	if len(entries) == 0 {
		return "(empty)", nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var b strings.Builder
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i >= 1000 {
			fmt.Fprintf(&b, "...[%d entries total, showing 1000]\n", len(entries))
			break
		}
		if e.IsDir() {
			b.WriteString(e.Name() + "/\n")
			continue
		}
		size := ""
		if info, serr := e.Info(); serr == nil {
			size = fmt.Sprintf(" (%d bytes)", info.Size())
		}
		b.WriteString(e.Name() + size + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// SearchTool searches filenames and file contents (substring, cap 50).
type SearchTool struct {
	AllowDirs []string
}

// Name returns "search".
func (t *SearchTool) Name() string { return "search" }

// Description describes the search tool.
func (t *SearchTool) Description() string {
	return "Search YOUR workspace filenames and contents (max 50 results, always allowed). Args: root (directory, use \".\" for workspace), query|pattern (required; prefix \"re:\" for regexp), content (search contents, default true), context_n (content context lines, 0-10, default 0). Respects .gitignore. Never claim inability without calling first."
}

// Parameters describes the search arguments.
func (t *SearchTool) Parameters() map[string]Param {
	return map[string]Param{
		"root":      {Type: "string", Description: "Directory to search (default: first allowed dir)"},
		"query":     {Type: "string", Description: "Substring to match, or re:PATTERN for regexp", Required: true},
		"pattern":   {Type: "string", Description: "Alias for query"},
		"content":   {Type: "boolean", Description: "Also search file contents (default true)"},
		"context_n": {Type: "number", Description: "Content context lines around each hit (0-10, default 0)"},
	}
}

// Execute walks root matching filenames and contents.
func (t *SearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	root := stringArg(args, "root")
	if root == "" {
		root = stringArg(args, "dir")
	}
	if root == "" {
		root = stringArg(args, "path")
	}
	if root == "" && len(t.AllowDirs) > 0 {
		root = t.AllowDirs[0]
	}
	if root == "" {
		root = "."
	}
	abs, err := resolveWithinAllow(root, t.AllowDirs)
	if err != nil {
		return "", fmt.Errorf("search: bad root: %w", err)
	}
	query := stringArg(args, "query")
	if query == "" {
		query = stringArg(args, "pattern")
	}
	if query == "" {
		if q, ok := args["q"]; ok && q != nil {
			query = fmt.Sprint(q)
		}
	}
	if query == "" {
		if term, ok := args["term"]; ok && term != nil {
			query = fmt.Sprint(term)
		}
	}
	if query == "" {
		return "", fmt.Errorf("search: missing query")
	}
	withContent := true
	if v, ok := args["content"]; ok && v != nil {
		switch c := v.(type) {
		case bool:
			withContent = c
		case string:
			l := strings.ToLower(strings.TrimSpace(c))
			withContent = l != "false" && l != "0" && l != "no"
		}
	}
	contextN := 0
	if v, ok := numberArg(args, "context_n"); ok && v > 0 {
		contextN = int(v)
		if contextN > 10 {
			contextN = 10
		}
	}
	// "re:" prefix switches filename and content matching to regexp.
	var re *regexp.Regexp
	if strings.HasPrefix(query, "re:") {
		pat := strings.TrimPrefix(query, "re:")
		if strings.TrimSpace(pat) == "" {
			return "", fmt.Errorf("search: empty regexp after \"re:\"")
		}
		var err error
		re, err = regexp.Compile(pat)
		if err != nil {
			return "", fmt.Errorf("search: bad regexp: %w", err)
		}
	}
	matchName := func(name string) bool {
		if re != nil {
			return re.MatchString(name)
		}
		return strings.Contains(name, query)
	}
	// .gitignore at the search root prunes ignored files and dirs.
	ig, _ := LoadDir(abs)

	var results []string
	add := func(s string) bool {
		if len(results) >= SearchCap {
			return false
		}
		results = append(results, s)
		return true
	}
	werr := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" && p != abs {
				return filepath.SkipDir
			}
			if p != abs {
				if rel, rerr := filepath.Rel(abs, p); rerr == nil && ig.Matched(rel) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel := p
		if r, rerr := filepath.Rel(abs, p); rerr == nil {
			rel = r
		}
		if ig.Matched(rel) {
			return nil
		}
		if matchName(d.Name()) {
			if !add(rel) {
				return errSearchCap
			}
		}
		if withContent {
			matches, merr := contentMatchesEx(p, query, re, 3, contextN)
			if merr != nil {
				return nil
			}
			for _, m := range matches {
				if !add(rel + ":" + m) {
					return errSearchCap
				}
			}
		}
		return nil
	})
	if werr != nil && !errors.Is(werr, errSearchCap) && !errors.Is(werr, context.Canceled) && !errors.Is(werr, context.DeadlineExceeded) {
		// Walk-level fatal error (e.g. root vanished); report only if no results.
		if len(results) == 0 {
			return "", fmt.Errorf("search: %w", werr)
		}
	}
	if ctx.Err() != nil && len(results) == 0 {
		return "", ctx.Err()
	}
	if len(results) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(results, "\n"), nil
}

// contentMatches returns up to maxLines "lineno:text" content hits.
// Files over 512KB, unreadable files, and binary files are skipped.
func contentMatches(path, query string, maxLines int) ([]string, error) {
	return contentMatchesEx(path, query, nil, maxLines, 0)
}

// contentMatchesEx matches file content by substring (re == nil) or regexp,
// returning up to maxLines hits. With contextN > 0, each hit is emitted with
// ±contextN surrounding lines: hits as "lineno:text", context as
// "lineno~text" (tilde marks non-hit context, windows merged in order).
func contentMatchesEx(path, query string, re *regexp.Regexp, maxLines, contextN int) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > 512*1024 {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if isBinary(data) {
		return nil, nil
	}
	hit := func(ln string) bool {
		if re != nil {
			return re.MatchString(ln)
		}
		return strings.Contains(ln, query)
	}
	lines := strings.Split(string(data), "\n")
	var hitIdx []int
	for i, ln := range lines {
		if hit(ln) {
			hitIdx = append(hitIdx, i)
			if len(hitIdx) >= maxLines {
				break
			}
		}
	}
	var out []string
	emit := map[int]bool{}
	short := func(s string) string {
		t := strings.TrimSpace(s)
		if len(t) > 200 {
			t = t[:200]
		}
		return t
	}
	for _, h := range hitIdx {
		lo, hi := h, h
		if contextN > 0 {
			lo = h - contextN
			if lo < 0 {
				lo = 0
			}
			hi = h + contextN
			if hi >= len(lines) {
				hi = len(lines) - 1
			}
		}
		for i := lo; i <= hi; i++ {
			if emit[i] {
				continue
			}
			emit[i] = true
			if i == h {
				out = append(out, fmt.Sprintf("%d:%s", i+1, short(lines[i])))
			} else {
				out = append(out, fmt.Sprintf("%d~%s", i+1, short(lines[i])))
			}
		}
	}
	return out, nil
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
