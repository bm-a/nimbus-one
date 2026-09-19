package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldFlush(t *testing.T) {
	cases := []struct {
		name                 string
		bytes, thresh, floor int
		want                 bool
	}{
		{"well below threshold", 100, 1000, 0, false},
		{"just below margin trigger", 899, 1000, 0, false},
		{"at margin trigger (900 = 1000-100)", 900, 1000, 0, true},
		{"at threshold", 1000, 1000, 0, true},
		{"over threshold", 5000, 1000, 0, true},
		{"force floor fires despite high threshold", 500, 100000, 400, true},
		{"below floor stays quiet", 399, 100000, 400, false},
		{"zero threshold disables soft trigger", 500, 0, 0, false},
		{"zero threshold with floor", 500, 0, 400, true},
		{"zero bytes never flushes", 0, 1000, 0, false},
	}
	for _, c := range cases {
		if got := ShouldFlush(c.bytes, c.thresh, c.floor); got != c.want {
			t.Errorf("%s: ShouldFlush(%d,%d,%d)=%v want %v",
				c.name, c.bytes, c.thresh, c.floor, got, c.want)
		}
	}
}

func TestFlushFile(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"user: hello", "assistant: world"}
	path, err := FlushFile(dir, "agent:worker-1:main", lines)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(dir, "memory") {
		t.Fatalf("bad dir: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"Session Key: agent:worker-1:main",
		"Session ID: worker-1",
		"Reason:",
		"user: hello",
		"assistant: world",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	// Same-minute second flush collides -> -2 suffix.
	second, err := FlushFile(dir, "agent:worker-1:main", lines)
	if err != nil {
		t.Fatal(err)
	}
	if second == path {
		t.Fatal("collision not disambiguated")
	}
	if !strings.HasSuffix(second, "-2.md") {
		t.Fatalf("want -2 suffix, got %s", second)
	}
	if _, err := FlushFile(dir, "bogus-key", lines); err == nil {
		t.Fatal("expected invalid-key error")
	}
}
