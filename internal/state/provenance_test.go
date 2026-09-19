package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTracked(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProvenanceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeTracked(t, dir, "memory/note.md", "hello")
	if err := Record(dir, "memory/note.md", "agent"); err != nil {
		t.Fatal(err)
	}
	rec, err := Read(dir, "memory/note.md")
	if err != nil {
		t.Fatal(err)
	}
	if rec.OriginClass != OriginAgent {
		t.Fatalf("want agent, got %q", rec.OriginClass)
	}
	if len(rec.FileHash) != 64 {
		t.Fatalf("want sha256 hex, got %q", rec.FileHash)
	}
	if rec.ObservedAt.IsZero() || time.Since(rec.ObservedAt) > time.Minute {
		t.Fatalf("bad observedAt: %v", rec.ObservedAt)
	}
	if !Verify(dir, "memory/note.md") {
		t.Fatal("fresh record should verify")
	}
}

func TestProvenanceUntrusted(t *testing.T) {
	dir := t.TempDir()
	writeTracked(t, dir, "inbox/scrape.md", "foreign content")
	for _, origin := range []string{"web", "user", "", "AGENTX", "tool:fetch"} {
		if err := Record(dir, "inbox/scrape.md", origin); err != nil {
			t.Fatal(err)
		}
		rec, err := Read(dir, "inbox/scrape.md")
		if err != nil {
			t.Fatal(err)
		}
		if rec.OriginClass != OriginUntrusted {
			t.Fatalf("origin %q: want untrusted, got %q", origin, rec.OriginClass)
		}
	}
	// Case-insensitive "agent" stays agent.
	if err := Record(dir, "inbox/scrape.md", "Agent"); err != nil {
		t.Fatal(err)
	}
	rec, _ := Read(dir, "inbox/scrape.md")
	if rec.OriginClass != OriginAgent {
		t.Fatalf("want agent, got %q", rec.OriginClass)
	}
}

func TestProvenanceTamper(t *testing.T) {
	dir := t.TempDir()
	p := writeTracked(t, dir, "memory/note.md", "original")
	if err := Record(dir, "memory/note.md", "agent"); err != nil {
		t.Fatal(err)
	}
	// Tamper with the tracked file.
	if err := os.WriteFile(p, []byte("modified by attacker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Verify(dir, "memory/note.md") {
		t.Fatal("tampered file verified — tamper detection broken")
	}
	// Missing file / missing sidecar both fail closed.
	if Verify(dir, "memory/ghost.md") {
		t.Fatal("missing file verified")
	}
	writeTracked(t, dir, "memory/nosidecar.md", "x")
	if Verify(dir, "memory/nosidecar.md") {
		t.Fatal("missing sidecar verified")
	}
	// Corrupt sidecar fails closed on Read too.
	if err := os.WriteFile(p+".prov", []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir, "memory/note.md"); err == nil {
		t.Fatal("expected corrupt-sidecar error")
	}
	if Verify(dir, "memory/note.md") {
		t.Fatal("corrupt sidecar verified")
	}
}

func TestProvenancePathSafety(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"../escape.md", "/abs.md", ".."} {
		if err := Record(dir, bad, "agent"); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
		if Verify(dir, bad) {
			t.Fatalf("verify true for %q", bad)
		}
	}
}
