// Sandbox drift audit: lightweight honesty layer for write/edit policy.
//
// OpenClaw reference (read-only):
//
//	sandbox/config.ts (mode/backend/scope, capDrop, readOnlyRoot) +
//	exec-filesystem-policy.ts (drift audit). Nimbus port: an in-memory
//	policy flag plus an append-only audit ring (last 100 exec-vs-policy
//	events). No enforcement daemon — refusal errors are explicit.
package tools

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Drift holds filesystem mutation policy flags.
type Drift struct {
	// WriteDisabled refuses write-tool mutations when true.
	WriteDisabled bool
	// EditDisabled refuses edit-tool mutations when true.
	EditDisabled bool
	// Reason is surfaced in refusal errors (e.g. "read-only root").
	Reason string
}

// Policy is the process-wide mutation policy consulted by write/edit tools.
// Default allows everything; tests and locked-down modes flip the flags.
var Policy = &Drift{}

// CheckWrite refuses path writes when the policy disables them.
func (d *Drift) CheckWrite(path string) error {
	if d != nil && d.WriteDisabled {
		return fmt.Errorf("write refused by policy%s: %s", d.suffix(), path)
	}
	return nil
}

// CheckEdit refuses path edits when the policy disables them.
func (d *Drift) CheckEdit(path string) error {
	if d != nil && d.EditDisabled {
		return fmt.Errorf("edit refused by policy%s: %s", d.suffix(), path)
	}
	return nil
}

func (d *Drift) suffix() string {
	if d != nil && strings.TrimSpace(d.Reason) != "" {
		return " (" + d.Reason + ")"
	}
	return ""
}

// AuditEvent is one exec-vs-policy decision record.
type AuditEvent struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`   // e.g. "exec", "write", "edit"
	Detail  string    `json:"detail"` // command or path
	Allowed bool      `json:"allowed"`
	Reason  string    `json:"reason,omitempty"`
}

// AuditMaxEvents caps the in-memory ring.
const AuditMaxEvents = 100

// AuditLog is an append-only in-memory ring of the last AuditMaxEvents events.
type AuditLog struct {
	mu     sync.Mutex
	events []AuditEvent
}

// DefaultAudit collects exec-vs-policy events for the process.
var DefaultAudit = &AuditLog{}

// Record appends an event, evicting the oldest beyond the cap.
func (l *AuditLog) Record(kind, detail string, allowed bool, reason string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, AuditEvent{
		Time: time.Now(), Kind: kind, Detail: detail, Allowed: allowed, Reason: reason,
	})
	if len(l.events) > AuditMaxEvents {
		l.events = append([]AuditEvent{}, l.events[len(l.events)-AuditMaxEvents:]...)
	}
}

// Recent returns a copy of buffered events (oldest first).
func (l *AuditLog) Recent() []AuditEvent {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEvent, len(l.events))
	copy(out, l.events)
	return out
}

// Len returns the buffered event count.
func (l *AuditLog) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}
