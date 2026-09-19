// Package context owns the agent's context budget: output truncation with
// file spillover (Phase 1), token estimation and compaction (Phase 2).
//
// Spillover rule: no tool output is silently cut. Oversized output lands
// in a temp file and the model gets a pointer it can page through with
// the read tool — the OpenCode truncate.output → outputPath pattern.
package context

import (
	"fmt"
	"os"
	"strings"
)

// DefaultSpillLimit caps inline tool output before spilling.
const DefaultSpillLimit = 12 * 1024

// Spill returns text inline when within limit, else writes the full text
// to a temp file and returns a pointer summary. The prefix identifies the
// producing tool for the model's paging decision.
func Spill(prefix, text string, limit int) string {
	if limit <= 0 {
		limit = DefaultSpillLimit
	}
	if len(text) <= limit {
		return text
	}
	f, err := os.CreateTemp("", "nimbus-spill-*.txt")
	if err != nil {
		// No temp space: hard-truncate, but say so explicitly.
		return text[:limit] + fmt.Sprintf("\n...[truncated %d bytes: temp file unavailable: %v]", len(text)-limit, err)
	}
	_, _ = f.WriteString(text)
	_ = f.Close()
	head := text
	if idx := strings.Index(head, "\n"); idx >= 0 && idx < 2000 {
		head = head[:idx]
	}
	if len(head) > 500 {
		head = head[:500]
	}
	return fmt.Sprintf("%s\n...[output %d bytes exceeds %d inline limit; full output spilled to %s — page it with read in chunks]",
		strings.TrimRight(head, "\n"), len(text), limit, f.Name())
}
