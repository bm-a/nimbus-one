package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashEcho(t *testing.T) {
	tool := &BashTool{}
	out, err := tool.Execute(context.Background(), map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("output %q does not contain hello", out)
	}
}

func TestBashEmptyCommandErrors(t *testing.T) {
	tool := &BashTool{}
	if _, err := tool.Execute(context.Background(), map[string]any{"command": ""}); err == nil {
		t.Fatal("expected error for empty command")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestRegisterBuiltinsNames(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r, t.TempDir())
	want := []string{"bash", "read", "write", "list", "search", "web_fetch", "web_search"}
	names := map[string]bool{}
	for _, n := range r.Names() {
		names[n] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Fatalf("RegisterBuiltins names %v missing %q", r.Names(), w)
		}
	}
}

func TestRelativePathsResolveToWorkspace(t *testing.T) {
	dir := t.TempDir()
	allow := []string{dir}
	ctx := context.Background()
	w := &WriteTool{AllowDirs: allow}
	if _, err := w.Execute(ctx, map[string]any{"path": "rel.txt", "content": "x"}); err != nil {
		t.Fatalf("relative write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rel.txt")); err != nil {
		t.Fatalf("relative path must land in workspace: %v", err)
	}
	l := &ListTool{AllowDirs: allow}
	out, err := l.Execute(ctx, map[string]any{"path": "."})
	if err != nil || !strings.Contains(out, "rel.txt") {
		t.Fatalf("list . must show workspace: out=%q err=%v", out, err)
	}
	// Escape still rejected.
	if _, err := w.Execute(ctx, map[string]any{"path": "../escape.txt", "content": "x"}); err == nil {
		t.Fatal("path escape must be rejected")
	}
	// Stray whitespace (a chronic model habit) is trimmed, not ENOENT.
	out, err = l.Execute(ctx, map[string]any{"path": ". "})
	if err != nil || !strings.Contains(out, "rel.txt") {
		t.Fatalf("list '. ' must trim and show workspace: out=%q err=%v", out, err)
	}
}

func TestReadWriteListRoundtrip(t *testing.T) {
	dir := t.TempDir()
	allow := []string{dir}
	w := &WriteTool{AllowDirs: allow}
	r := &ReadTool{AllowDirs: allow}
	l := &ListTool{AllowDirs: allow}
	ctx := context.Background()

	fp := filepath.Join(dir, "sub", "hello.txt")
	msg, err := w.Execute(ctx, map[string]any{"path": fp, "content": "roundtrip-content"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(msg, fp) {
		t.Fatalf("write message %q missing path", msg)
	}
	got, err := r.Execute(ctx, map[string]any{"path": fp})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "roundtrip-content" {
		t.Fatalf("Read = %q, want roundtrip-content", got)
	}
	out, err := l.Execute(ctx, map[string]any{"path": filepath.Join(dir, "sub")})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(out, "hello.txt") {
		t.Fatalf("List = %q, want hello.txt", out)
	}
}

func TestSearchFindsContentMatch(t *testing.T) {
	dir := t.TempDir()
	allow := []string{dir}
	ctx := context.Background()
	w := &WriteTool{AllowDirs: allow}
	if _, err := w.Execute(ctx, map[string]any{"path": filepath.Join(dir, "a.txt"), "content": "uniqueneedle123 inside"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Execute(ctx, map[string]any{"path": filepath.Join(dir, "b.txt"), "content": "nothing here"}); err != nil {
		t.Fatal(err)
	}
	s := &SearchTool{AllowDirs: allow}
	out, err := s.Execute(ctx, map[string]any{"root": dir, "query": "uniqueneedle123"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("Search = %q, want a.txt match", out)
	}
}

func TestWriteEscapesAllowDirs(t *testing.T) {
	dir := t.TempDir()
	w := &WriteTool{AllowDirs: []string{dir}}
	outside := filepath.Join(t.TempDir(), "evil.txt")
	if _, err := w.Execute(context.Background(), map[string]any{"path": outside, "content": "x"}); err == nil {
		t.Fatalf("expected error writing outside allowed dirs (outside=%s allow=%s)", outside, dir)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("file outside allowed dirs should not have been created: %s", outside)
	}
}

func TestFetchToolAgainstHttptest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>T</title></head><body><p>Hello Fetch World</p><script>var x=1;</script></body></html>"))
	}))
	defer srv.Close()

	f := &FetchTool{}
	out, err := f.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(out, "Hello Fetch World") {
		t.Fatalf("Fetch = %q, want text present", out)
	}
	if strings.Contains(out, "var x=1") {
		t.Fatalf("Fetch = %q, script content should be stripped", out)
	}
	if strings.Contains(out, "<p>") {
		t.Fatalf("Fetch = %q, HTML tags should be stripped", out)
	}
}

func TestFetchToolBadScheme(t *testing.T) {
	f := &FetchTool{}
	if _, err := f.Execute(context.Background(), map[string]any{"url": "ftp://example.com/x"}); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestSearchTool2NameParamsOnly(t *testing.T) {
	s := &SearchTool2{}
	if s.Name() != "web_search" {
		t.Fatalf("Name = %q, want web_search", s.Name())
	}
	params := s.Parameters()
	if _, ok := params["query"]; !ok {
		t.Fatalf("Parameters missing query: %v", params)
	}
	if _, err := s.Execute(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected error for missing query (no network call)")
	}
}
