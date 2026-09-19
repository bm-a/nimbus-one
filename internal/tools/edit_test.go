package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMutationQueueSerializesSamePath(t *testing.T) {
	q := NewMutationQueue()
	const n = 20
	var mu sync.Mutex
	active := 0
	maxActive := 0
	counter := 0
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := q.Run("/same/path.txt", func() (string, error) {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				time.Sleep(2 * time.Millisecond)
				mu.Lock()
				counter++
				active--
				mu.Unlock()
				return "ok", nil
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Run[%d]: %v", i, err)
		}
	}
	if counter != n {
		t.Fatalf("counter = %d, want %d", counter, n)
	}
	if maxActive != 1 {
		t.Fatalf("max concurrency on same path = %d, want 1", maxActive)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("queue Len after release = %d, want 0", got)
	}
}

func TestMutationQueueDifferentPathsParallel(t *testing.T) {
	q := NewMutationQueue()
	start := make(chan struct{})
	entered := make(chan string, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for _, p := range []string{"/a.txt", "/b.txt"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_, _ = q.Run(p, func() (string, error) {
				entered <- p
				<-release
				return p, nil
			})
		}(p)
	}
	close(start)
	_ = start
	got := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for len(got) < 2 {
		select {
		case p := <-entered:
			got[p] = true
		case <-timeout:
			close(release)
			wg.Wait()
			t.Fatalf("different paths must run concurrently, entered=%v", got)
		}
	}
	close(release)
	wg.Wait()
}

func TestMutationQueuePropagatesError(t *testing.T) {
	q := NewMutationQueue()
	if _, err := q.Run("/e.txt", func() (string, error) {
		return "", context.DeadlineExceeded
	}); err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestEditToolApplyAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	tool := &EditTool{AllowDirs: []string{dir}}
	ctx := context.Background()
	fp := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(fp, []byte("hello world\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(ctx, map[string]any{
		"path":  fp,
		"edits": []any{map[string]any{"oldText": "hello world", "newText": "hello nimbus"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "applied 1 change") || !strings.Contains(out, "- hello world") {
		t.Fatalf("out missing diff preview: %q", out)
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "hello nimbus\nsecond line\n" {
		t.Fatalf("content = %q", data)
	}
	// Re-applying the same edit: oldText no longer matches → mismatch error.
	if _, err := tool.Execute(ctx, map[string]any{
		"path":  fp,
		"edits": []any{map[string]any{"oldText": "hello world", "newText": "hello nimbus"}},
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
	// No-op receipt: oldText == newText touches nothing.
	before, _ := os.Stat(fp)
	out, err = tool.Execute(ctx, map[string]any{
		"path":  fp,
		"edits": []any{map[string]any{"oldText": "hello nimbus", "newText": "hello nimbus"}},
	})
	if err != nil {
		t.Fatalf("no-op: %v", err)
	}
	if !strings.Contains(out, "no-op receipt") {
		t.Fatalf("out = %q, want no-op receipt", out)
	}
	after, _ := os.Stat(fp)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("no-op edit must not rewrite the file")
	}
}

func TestEditToolMismatchIncludesSnippet(t *testing.T) {
	dir := t.TempDir()
	tool := &EditTool{AllowDirs: []string{dir}}
	fp := filepath.Join(dir, "b.txt")
	_ = os.WriteFile(fp, []byte("line one\nline two\n"), 0o644)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":  fp,
		"edits": []any{map[string]any{"oldText": "absent-needle", "newText": "x"}},
	})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "1| line one") {
		t.Fatalf("error must carry file snippet: %v", err)
	}
}

func TestEditToolAmbiguousMatchErrors(t *testing.T) {
	dir := t.TempDir()
	tool := &EditTool{AllowDirs: []string{dir}}
	fp := filepath.Join(dir, "c.txt")
	_ = os.WriteFile(fp, []byte("dup\ndup\n"), 0o644)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":  fp,
		"edits": []any{map[string]any{"oldText": "dup", "newText": "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestEditToolPreservesBOMAndCRLF(t *testing.T) {
	dir := t.TempDir()
	tool := &EditTool{AllowDirs: []string{dir}}
	fp := filepath.Join(dir, "d.txt")
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("a\r\nb\r\n")...)
	if err := os.WriteFile(fp, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":  fp,
		"edits": []any{map[string]any{"oldText": "a\r\nb", "newText": "A\r\nB"}},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, _ := os.ReadFile(fp)
	want := append([]byte{0xEF, 0xBB, 0xBF}, []byte("A\r\nB\r\n")...)
	if string(got) != string(want) {
		t.Fatalf("got %q want BOM+CRLF preserved", got)
	}
}

func TestEditToolRejectsOutsideAllow(t *testing.T) {
	tool := &EditTool{AllowDirs: []string{t.TempDir()}}
	outside := filepath.Join(t.TempDir(), "evil.txt")
	_ = os.WriteFile(outside, []byte("x"), 0o644)
	if _, err := tool.Execute(context.Background(), map[string]any{
		"path":  outside,
		"edits": []any{map[string]any{"oldText": "x", "newText": "y"}},
	}); err == nil {
		t.Fatal("expected allow-dirs rejection")
	}
}

func TestEditToolMissingEditsErrors(t *testing.T) {
	tool := &EditTool{AllowDirs: []string{t.TempDir()}}
	if _, err := tool.Execute(context.Background(), map[string]any{"path": "f.txt"}); err == nil {
		t.Fatal("expected error for missing edits")
	}
}
