package context

import (
	"strings"
	"testing"
)

func TestSpillInline(t *testing.T) {
	if got := Spill("bash", "short", 100); got != "short" {
		t.Fatalf("inline = %q", got)
	}
}

func TestSpillPointer(t *testing.T) {
	big := strings.Repeat("x", 5000)
	got := Spill("bash", big, 100)
	if !strings.Contains(got, "spilled to ") || !strings.Contains(got, "read in chunks") {
		t.Fatalf("spill = %q", got)
	}
	if len(got) > 2000 {
		t.Fatalf("spill pointer too long: %d", len(got))
	}
}
