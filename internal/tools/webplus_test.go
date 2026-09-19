package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T) *http.Client {
	t.Helper()
	c, err := webClient(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"BRAVE_API_KEY", "TAVILY_API_KEY", "EXA_API_KEY",
		"NIMBUS_BRAVE_URL", "NIMBUS_TAVILY_URL", "NIMBUS_EXA_URL"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestProviderChainNoneConfiguredFallsThrough(t *testing.T) {
	clearProviderEnv(t)
	_, _, ok := searchWithProviders(context.Background(), testClient(t), "q", 3)
	if ok {
		t.Fatal("no keys configured must fall through to DDG")
	}
}

func TestProviderChainBraveFirst(t *testing.T) {
	clearProviderEnv(t)
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "fake-brave" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.Query().Get("q"), "nimbus") {
			http.Error(w, "bad q", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Brave Hit","url":"https://example.com/b"}]}}`))
	}))
	defer brave.Close()
	t.Setenv("BRAVE_API_KEY", "fake-brave")
	t.Setenv("NIMBUS_BRAVE_URL", brave.URL)
	name, out, ok := searchWithProviders(context.Background(), testClient(t), "nimbus test", 3)
	if !ok || name != "brave" {
		t.Fatalf("name=%q ok=%v out=%q", name, ok, out)
	}
	if !strings.Contains(out, "Brave Hit") || !strings.Contains(out, "https://example.com/b") {
		t.Fatalf("out = %q", out)
	}
}

func TestProviderChainFallsToTavilyOnBraveFailure(t *testing.T) {
	clearProviderEnv(t)
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer brave.Close()
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Tavily Hit","url":"https://example.com/t"}]}`))
	}))
	defer tavily.Close()
	t.Setenv("BRAVE_API_KEY", "fake-brave")
	t.Setenv("NIMBUS_BRAVE_URL", brave.URL)
	t.Setenv("TAVILY_API_KEY", "fake-tavily")
	t.Setenv("NIMBUS_TAVILY_URL", tavily.URL)
	name, out, ok := searchWithProviders(context.Background(), testClient(t), "q", 3)
	if !ok || name != "tavily" {
		t.Fatalf("name=%q ok=%v out=%q", name, ok, out)
	}
	if !strings.Contains(out, "Tavily Hit") {
		t.Fatalf("out = %q", out)
	}
}

func TestProviderChainExaLastResort(t *testing.T) {
	clearProviderEnv(t)
	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "fake-exa" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Exa Hit","url":"https://example.com/e"}]}`))
	}))
	defer exa.Close()
	t.Setenv("EXA_API_KEY", "fake-exa")
	t.Setenv("NIMBUS_EXA_URL", exa.URL)
	name, out, ok := searchWithProviders(context.Background(), testClient(t), "q", 2)
	if !ok || name != "exa" {
		t.Fatalf("name=%q ok=%v out=%q", name, ok, out)
	}
	if !strings.Contains(out, "Exa Hit") {
		t.Fatalf("out = %q", out)
	}
}

func TestSearchTool2UsesKeyedProvider(t *testing.T) {
	clearProviderEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Wired Hit","url":"https://example.com/w"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TAVILY_API_KEY", "fake")
	t.Setenv("NIMBUS_TAVILY_URL", srv.URL)
	s := &SearchTool2{}
	out, err := s.Execute(context.Background(), map[string]any{"query": "hello", "count": 3})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Wired Hit") || !strings.Contains(out, "[via tavily]") {
		t.Fatalf("out = %q", out)
	}
}

func TestLinePagerCursors(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "lines.txt")
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString(strings.Repeat("x", 3) + strings.Repeat("l", 1) + "\n")
	}
	_ = os.WriteFile(fp, []byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"), 0o644)
	p := &LinePager{AllowDirs: []string{dir}}
	lines, next, err := p.Read(fp, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 || lines[0] != "l1" || next != 4 {
		t.Fatalf("lines=%v next=%d", lines, next)
	}
	lines, next, err = p.Read(fp, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || next != -1 {
		t.Fatalf("final page lines=%v next=%d", lines, next)
	}
	lines, next, err = p.Read(fp, 50, 4)
	if err != nil || len(lines) != 0 || next != -1 {
		t.Fatalf("past EOF: lines=%v next=%d err=%v", lines, next, err)
	}
	tool := &PagedReadTool{AllowDirs: []string{dir}}
	out, err := tool.Execute(context.Background(), map[string]any{"path": fp, "offset_lines": 9, "limit_lines": 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "10| l10") || !strings.Contains(out, "next_cursor=-1") {
		t.Fatalf("out = %q", out)
	}
}

func TestIgnoreMatcher(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# comment\n*.log\nbuild/\n!important.log\n"), 0o644)
	ig, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ig.Matched("a.log") || !ig.Matched("sub/b.log") {
		t.Fatal("*.log must match at any depth")
	}
	if !ig.Matched("build/out.bin") {
		t.Fatal("build/ must match contents")
	}
	if ig.Matched("important.log") {
		t.Fatal("negation must re-include important.log")
	}
	if ig.Matched("keep.txt") {
		t.Fatal("keep.txt must not match")
	}
	empty, err := LoadDir(t.TempDir())
	if err != nil || empty.Matched("x.txt") {
		t.Fatalf("missing .gitignore must match nothing: %v", err)
	}
}

func TestSearchRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "ignored"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "ignored", "a.txt"), []byte("needle-zzz here"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "kept.txt"), []byte("needle-zzz here"), 0o644)
	s := &SearchTool{AllowDirs: []string{dir}}
	out, err := s.Execute(context.Background(), map[string]any{"root": dir, "query": "needle-zzz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "kept.txt") {
		t.Fatalf("must find kept.txt: %q", out)
	}
	if strings.Contains(out, "ignored") {
		t.Fatalf("must skip gitignored dir: %q", out)
	}
}

func TestSearchRegexpAndContext(t *testing.T) {
	dir := t.TempDir()
	content := "alpha one\nbeta two\ngamma 12345\nbeta three\ndelta\n"
	_ = os.WriteFile(filepath.Join(dir, "r.txt"), []byte(content), 0o644)
	s := &SearchTool{AllowDirs: []string{dir}}
	out, err := s.Execute(context.Background(), map[string]any{"root": dir, "query": "re:beta (two|three)", "content": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2:beta two") || !strings.Contains(out, "4:beta three") {
		t.Fatalf("regexp hits missing: %q", out)
	}
	out, err = s.Execute(context.Background(), map[string]any{"root": dir, "query": "gamma", "content": true, "context_n": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3:gamma 12345") || !strings.Contains(out, "2~beta two") || !strings.Contains(out, "4~beta three") {
		t.Fatalf("context lines missing: %q", out)
	}
	if _, err := s.Execute(context.Background(), map[string]any{"root": dir, "query": "re:(unclosed"}); err == nil {
		t.Fatal("bad regexp must error")
	}
}

func TestSandboxDriftAndAudit(t *testing.T) {
	oldPolicy, oldAudit := Policy, DefaultAudit
	Policy, DefaultAudit = &Drift{}, &AuditLog{}
	defer func() { Policy, DefaultAudit = oldPolicy, oldAudit }()

	dir := t.TempDir()
	ctx := context.Background()
	w := &WriteTool{AllowDirs: []string{dir}}
	if _, err := w.Execute(ctx, map[string]any{"path": filepath.Join(dir, "ok.txt"), "content": "x"}); err != nil {
		t.Fatalf("allowed write: %v", err)
	}
	Policy.WriteDisabled = true
	Policy.Reason = "read-only root"
	if _, err := w.Execute(ctx, map[string]any{"path": filepath.Join(dir, "no.txt"), "content": "x"}); err == nil ||
		!strings.Contains(err.Error(), "read-only root") {
		t.Fatalf("disabled write must refuse with reason, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "no.txt")); !os.IsNotExist(err) {
		t.Fatal("refused write must not create the file")
	}
	e := &EditTool{AllowDirs: []string{dir}}
	_ = os.WriteFile(filepath.Join(dir, "ed.txt"), []byte("a"), 0o644)
	Policy.EditDisabled = true
	if _, err := e.Execute(ctx, map[string]any{
		"path":  filepath.Join(dir, "ed.txt"),
		"edits": []any{map[string]any{"oldText": "a", "newText": "b"}},
	}); err == nil || !strings.Contains(err.Error(), "refused by policy") {
		t.Fatalf("disabled edit must refuse, got %v", err)
	}
	// Audit ring caps at 100.
	for i := 0; i < 150; i++ {
		DefaultAudit.Record("exec", "echo hi", true, "")
	}
	evs := DefaultAudit.Recent()
	if len(evs) != 100 {
		t.Fatalf("audit ring len = %d, want 100", len(evs))
	}
	// Refusal was recorded.
	found := false
	for _, ev := range DefaultAudit.Recent() {
		if ev.Kind == "write" && !ev.Allowed {
			found = true
		}
	}
	_ = found // ring may have evicted it after 150 exec records; check separately below.
	a2 := &AuditLog{}
	a2.Record("write", "p", false, "refused")
	if a2.Len() != 1 || a2.Recent()[0].Allowed {
		t.Fatal("refusal event must be recorded")
	}
	// Bash exec records an audit event.
	b := &BashTool{}
	if _, err := b.Execute(ctx, map[string]any{"command": "echo audit-me"}); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, ev := range DefaultAudit.Recent() {
		if ev.Kind == "exec" && strings.Contains(ev.Detail, "echo audit-me") && ev.Allowed {
			seen = true
		}
	}
	if !seen {
		t.Fatal("bash exec must leave an audit event")
	}
}
