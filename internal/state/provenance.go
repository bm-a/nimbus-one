// Per-file write-provenance sidecars.
//
// OpenClaw reference (read-only):
//
//	src/memory/memory-write-provenance.ts — every memory write records an
//	  originClass of agent (model-generated) or untrusted (external input)
//	  alongside the file, so later readers can distrust foreign content.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Origin classes recorded in provenance sidecars.
const (
	OriginAgent     = "agent"
	OriginUntrusted = "untrusted"
)

// Provenance is the sidecar record for one tracked file.
type Provenance struct {
	FileHash    string    `json:"fileHash"`
	OriginClass string    `json:"originClass"`
	ObservedAt  time.Time `json:"observedAt"`
}

// normalizeOrigin maps an origin label to its class: exactly "agent"
// (case-insensitive) stays agent; everything else is untrusted.
func normalizeOrigin(origin string) string {
	if strings.EqualFold(strings.TrimSpace(origin), OriginAgent) {
		return OriginAgent
	}
	return OriginUntrusted
}

// resolveProvTarget maps dir+relPath to the tracked file and its sidecar,
// rejecting absolute paths and paths escaping dir.
func resolveProvTarget(dir, relPath string) (target, sidecar string, err error) {
	clean := filepath.Clean(relPath)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("state: invalid provenance path %q", relPath)
	}
	target = filepath.Join(dir, clean)
	return target, target + ".prov", nil
}

// Record hashes the file at dir/relPath and writes its sidecar
// (<file>.prov JSON with fileHash sha256 hex, originClass, observedAt
// RFC3339). The tracked file must already exist.
func Record(dir, relPath, origin string) error {
	target, sidecar, err := resolveProvTarget(dir, relPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	rec := Provenance{
		FileHash:    hex.EncodeToString(sum[:]),
		OriginClass: normalizeOrigin(origin),
		ObservedAt:  time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(sidecar, raw, 0o644)
}

// Read returns the provenance sidecar for dir/relPath.
func Read(dir, relPath string) (Provenance, error) {
	_, sidecar, err := resolveProvTarget(dir, relPath)
	if err != nil {
		return Provenance{}, err
	}
	raw, err := os.ReadFile(sidecar)
	if err != nil {
		return Provenance{}, err
	}
	var rec Provenance
	if err := json.Unmarshal(raw, &rec); err != nil {
		return Provenance{}, fmt.Errorf("state: corrupt provenance sidecar for %q: %w", relPath, err)
	}
	if rec.FileHash == "" || rec.OriginClass == "" {
		return Provenance{}, fmt.Errorf("state: incomplete provenance sidecar for %q", relPath)
	}
	return rec, nil
}

// Verify re-hashes dir/relPath and compares it to the recorded sidecar.
// It returns false when the sidecar is missing/corrupt, the file is
// missing/unreadable, or the hashes differ (tamper detection).
func Verify(dir, relPath string) bool {
	rec, err := Read(dir, relPath)
	if err != nil {
		return false
	}
	target, _, err := resolveProvTarget(dir, relPath)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == rec.FileHash
}
