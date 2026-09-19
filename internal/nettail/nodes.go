package nettail

// File-backed pairing registry for remote nodes.
//
// OpenClaw reference (read-only):
//   - src/gateway/node-registry.ts (connected node sessions, caps/commands)
//   - src/infra/node-pairing-authz.ts + node-pairing-state (pairing codes,
//     approval surface, expiry)
//   - src/infra/node-pairing-surface.ts (caps/commands/permissions approval
//     surface intersection)
//   - src/gateway/node-command-policy.ts (capability/command allowlist)
//
// Pairing flow: Pair issues a 6-digit code valid for 10 minutes; Approve
// redeems it for a persistent node record. The store is JSON written with
// mode 0600 (secret-adjacent: pairing codes gate node admission).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PairTTL is how long a pairing code stays redeemable.
const PairTTL = 10 * time.Minute

// MaxNodes caps the persisted node list; oldest-seen entries are evicted first.
const MaxNodes = 256

// Node is a paired remote node record.
type Node struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Caps     []string  `json:"caps"`
	LastSeen time.Time `json:"last_seen"`
}

// pendingCode is an unredeemed pairing code.
type pendingCode struct {
	Code    string    `json:"code"`
	Name    string    `json:"name"`
	Caps    []string  `json:"caps"`
	Expires time.Time `json:"expires"`
}

// storeFile is the on-disk shape.
type storeFile struct {
	Nodes   []Node        `json:"nodes"`
	Pending []pendingCode `json:"pending"`
}

// Registry is a file-backed node pairing registry. The zero value is usable
// once Path is set; all methods are safe for concurrent use.
type Registry struct {
	Path string

	mu      sync.Mutex
	loaded  bool
	nodes   []Node
	pending []pendingCode
}

// load reads the store file if present. Callers must hold mu.
func (r *Registry) load() error {
	if r.loaded {
		return nil
	}
	r.loaded = true
	if r.Path == "" {
		return fmt.Errorf("nettail: node registry path is empty")
	}
	data, err := os.ReadFile(r.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("nettail: read node registry %q: %w", r.Path, err)
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return fmt.Errorf("nettail: parse node registry %q: %w", r.Path, err)
	}
	r.nodes = sf.Nodes
	r.pending = pruneExpired(sf.Pending, time.Now())
	return nil
}

// save writes the store file with mode 0600. Callers must hold mu.
func (r *Registry) save() error {
	if r.Path == "" {
		return fmt.Errorf("nettail: node registry path is empty")
	}
	if dir := filepath.Dir(r.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("nettail: create registry dir: %w", err)
		}
	}
	sf := storeFile{Nodes: r.nodes, Pending: r.pending}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("nettail: encode node registry: %w", err)
	}
	if err := os.WriteFile(r.Path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("nettail: write node registry %q: %w", r.Path, err)
	}
	return nil
}

// Pair issues a 6-digit pairing code for name valid for PairTTL.
// It returns "" when the name is empty or the store cannot be persisted.
func (r *Registry) Pair(name string, caps []string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return ""
	}
	now := time.Now()
	r.pending = pruneExpired(r.pending, now)
	code := newPairCode(r.pending)
	r.pending = append(r.pending, pendingCode{
		Code:    code,
		Name:    name,
		Caps:    append([]string(nil), caps...),
		Expires: now.Add(PairTTL),
	})
	if err := r.save(); err != nil {
		r.pending = removePending(r.pending, code)
		return ""
	}
	return code
}

// Approve redeems code for name, creating a persistent node. It returns
// false when the code is unknown, expired, or bound to another name.
func (r *Registry) Approve(code, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return false
	}
	now := time.Now()
	r.pending = pruneExpired(r.pending, now)
	idx := -1
	for i, p := range r.pending {
		if p.Code == code && p.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	p := r.pending[idx]
	r.pending = append(r.pending[:idx], r.pending[idx+1:]...)
	r.nodes = append(r.nodes, Node{
		ID:       newNodeID(),
		Name:     p.Name,
		Caps:     append([]string(nil), p.Caps...),
		LastSeen: now,
	})
	evictOldest(r.nodes)
	if err := r.save(); err != nil {
		return false
	}
	return true
}

// Nodes returns paired nodes ordered by most-recent LastSeen first,
// capped at MaxNodes.
func (r *Registry) Nodes() []Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return nil
	}
	out := append([]Node(nil), r.nodes...)
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	if len(out) > MaxNodes {
		out = out[:MaxNodes]
	}
	return out
}

// Touch refreshes LastSeen for id. It reports whether id was found.
func (r *Registry) Touch(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return false
	}
	for i, n := range r.nodes {
		if n.ID == id {
			r.nodes[i].LastSeen = time.Now()
			_ = r.save()
			return true
		}
	}
	return false
}

// Remove deletes the node with id. It reports whether id was found.
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.load(); err != nil {
		return false
	}
	for i, n := range r.nodes {
		if n.ID == id {
			r.nodes = append(r.nodes[:i], r.nodes[i+1:]...)
			_ = r.save()
			return true
		}
	}
	return false
}

// pruneExpired drops pending codes at or past now.
func pruneExpired(in []pendingCode, now time.Time) []pendingCode {
	out := in[:0]
	for _, p := range in {
		if p.Expires.After(now) {
			out = append(out, p)
		}
	}
	// Preserve nil-ness only when input was empty; callers tolerate either.
	if len(out) == 0 {
		return nil
	}
	return out
}

// removePending drops the entry with code.
func removePending(in []pendingCode, code string) []pendingCode {
	for i, p := range in {
		if p.Code == code {
			return append(in[:i], in[i+1:]...)
		}
	}
	return in
}

// evictOldest trims nodes to MaxNodes, dropping oldest LastSeen first.
func evictOldest(nodes []Node) []Node {
	if len(nodes) <= MaxNodes {
		return nodes
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].LastSeen.Before(nodes[j].LastSeen) })
	return nodes[len(nodes)-MaxNodes:]
}

// newPairCode generates a 6-digit code unique among pending.
func newPairCode(pending []pendingCode) string {
	taken := map[string]bool{}
	for _, p := range pending {
		taken[p.Code] = true
	}
	for {
		n, err := rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			// crypto/rand failure is unrecoverable for pairing; fall back
			// to a time-derived code rather than issuing a duplicate.
			return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
		}
		code := fmt.Sprintf("%06d", n.Int64())
		if !taken[code] {
			return code
		}
	}
}

// newNodeID generates a 16-hex-char node id.
func newNodeID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
