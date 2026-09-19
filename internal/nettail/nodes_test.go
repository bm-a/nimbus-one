package nettail

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	return &Registry{Path: filepath.Join(t.TempDir(), "nodes.json")}
}

func TestPairApprove(t *testing.T) {
	r := testRegistry(t)
	code := r.Pair("laptop", []string{"shell", "fs"})
	if len(code) != 6 {
		t.Fatalf("Pair code = %q, want 6 digits", code)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Fatalf("Pair code = %q, want digits only", code)
		}
	}
	if !r.Approve(code, "laptop") {
		t.Fatalf("Approve(valid code) = false, want true")
	}
	nodes := r.Nodes()
	if len(nodes) != 1 || nodes[0].Name != "laptop" {
		t.Fatalf("Nodes() = %+v, want one laptop node", nodes)
	}
	if len(nodes[0].Caps) != 2 || nodes[0].ID == "" {
		t.Fatalf("node record = %+v, want id and caps", nodes[0])
	}
	// Code is single-use.
	if r.Approve(code, "laptop") {
		t.Fatalf("Approve(reused code) = true, want false")
	}
}

func TestApproveWrong(t *testing.T) {
	r := testRegistry(t)
	code := r.Pair("laptop", nil)
	if r.Approve("000000", "laptop") {
		t.Fatalf("Approve(unknown code) = true, want false")
	}
	if r.Approve(code, "other-name") {
		t.Fatalf("Approve(wrong name) = true, want false")
	}
	if len(r.Nodes()) != 0 {
		t.Fatalf("Nodes() non-empty after failed approvals")
	}
}

func TestPairExpiry(t *testing.T) {
	r := testRegistry(t)
	code := r.Pair("laptop", nil)
	if code == "" {
		t.Fatalf("Pair returned empty code")
	}
	// Force expiry white-box (same package) instead of sleeping 10 minutes.
	r.mu.Lock()
	for i := range r.pending {
		if r.pending[i].Code == code {
			r.pending[i].Expires = time.Now().Add(-time.Minute)
		}
	}
	r.mu.Unlock()
	if r.Approve(code, "laptop") {
		t.Fatalf("Approve(expired code) = true, want false")
	}
}

func TestTouch(t *testing.T) {
	r := testRegistry(t)
	code := r.Pair("laptop", nil)
	if !r.Approve(code, "laptop") {
		t.Fatalf("Approve failed")
	}
	id := r.Nodes()[0].ID
	before := r.Nodes()[0].LastSeen
	time.Sleep(5 * time.Millisecond)
	if !r.Touch(id) {
		t.Fatalf("Touch(known id) = false, want true")
	}
	if got := r.Nodes()[0].LastSeen; !got.After(before) {
		t.Fatalf("Touch did not advance LastSeen: %v -> %v", before, got)
	}
	if r.Touch("no-such-id") {
		t.Fatalf("Touch(unknown id) = true, want false")
	}
}

func TestPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.json")
	r := &Registry{Path: path}
	code := r.Pair("laptop", []string{"shell"})
	if !r.Approve(code, "laptop") {
		t.Fatalf("Approve failed")
	}
	// Reload from disk in a fresh registry.
	r2 := &Registry{Path: path}
	nodes := r2.Nodes()
	if len(nodes) != 1 || nodes[0].Name != "laptop" {
		t.Fatalf("reloaded Nodes() = %+v, want one laptop node", nodes)
	}
	// Store file must be owner-only.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("store perm = %o, want 600", perm)
	}
}

func TestListCapped(t *testing.T) {
	r := testRegistry(t)
	if got := len(r.Nodes()); got != 0 {
		t.Fatalf("empty Nodes() = %d, want 0", got)
	}
	// Exercise the eviction helper directly at scale.
	nodes := make([]Node, 0, MaxNodes+10)
	base := time.Now()
	for i := 0; i < MaxNodes+10; i++ {
		nodes = append(nodes, Node{
			ID:       fmt.Sprintf("node-%04d", i),
			LastSeen: base.Add(time.Duration(i) * time.Second),
		})
	}
	trimmed := evictOldest(nodes)
	if len(trimmed) != MaxNodes {
		t.Fatalf("evictOldest len = %d, want %d", len(trimmed), MaxNodes)
	}
	// Oldest entries go first: survivor floor is node-0010.
	if trimmed[0].ID != "node-0010" {
		t.Fatalf("evictOldest[0] = %q, want node-0010", trimmed[0].ID)
	}
}
