// Memory flush policy + flush-file writer.
//
// OpenClaw reference (read-only):
//
//	src/agents/agent-runner-memory.ts — runMemoryFlushIfNeeded: flushes when
//	  the transcript passes the threshold minus a headroom margin, with an
//	  absolute force floor, skipping NO_REPLY turns.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// flushMarginDenominator sets the early-flush headroom: the soft trigger is
// threshold - threshold/denominator (i.e. 10% below threshold).
const flushMarginDenominator = 10

// ShouldFlush reports whether a memory flush is due.
//
// Soft trigger: transcriptBytes at or above thresholdBytes minus a 10%
// early-flush margin, so the flush lands before the hard limit.
// Hard trigger: forceFloor > 0 and transcriptBytes at or above forceFloor,
// an absolute cap that forces a flush regardless of the threshold.
// A non-positive thresholdBytes disables the soft trigger.
func ShouldFlush(transcriptBytes, thresholdBytes, forceFloor int) bool {
	if forceFloor > 0 && transcriptBytes >= forceFloor {
		return true
	}
	if thresholdBytes <= 0 {
		return false
	}
	trigger := thresholdBytes - thresholdBytes/flushMarginDenominator
	if trigger < 0 {
		trigger = 0
	}
	return transcriptBytes >= trigger
}

// FlushFile writes lines as a dated memory file under dir/memory/ named
// YYYY-MM-DD-HHMM.md, with Session Key / Session ID / Reason headers.
// On filename collision it appends a -N suffix (base-2.md, base-3.md, ...).
// It returns the written file path. The session key is parsed with
// ParseSessionKey, so only agent:<id>:<rest> keys are accepted.
func FlushFile(dir, sessionKey string, lines []string) (string, error) {
	agentID, _, err := ParseSessionKey(sessionKey)
	if err != nil {
		return "", err
	}
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("2006-01-02-1504")
	path := filepath.Join(memDir, stamp+".md")
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", err
		}
		path = filepath.Join(memDir, fmt.Sprintf("%s-%d.md", stamp, i))
	}
	var sb strings.Builder
	sb.WriteString("# Memory Flush\n\n")
	sb.WriteString("Session Key: " + sessionKey + "\n")
	sb.WriteString("Session ID: " + agentID + "\n")
	fmt.Fprintf(&sb, "Reason: transcript flush (%d lines)\n", len(lines))
	sb.WriteString("Created: " + time.Now().UTC().Format(time.RFC3339) + "\n\n")
	sb.WriteString("## Transcript\n\n")
	for _, ln := range lines {
		sb.WriteString(ln + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
