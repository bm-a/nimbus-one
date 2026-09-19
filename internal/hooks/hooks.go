// Package hooks is a fan-out event system for agent lifecycle events.
//
// Mirrors OpenClaw (read-only reference in /data/.../tmp/openclaw-src):
//   - src/hooks/internal-hooks.ts (register/trigger fan-out, no
//     short-circuit: every matching handler runs even if an earlier one
//     fails) plus the internal hook event keys and HOOK.md loader.
//
// Registration keying supports family prefixes: a handler registered for
// "session" also fires for events keyed "session:auto-reset".
//
// NOTE on the handler signature: handlers return a string message ("" means
// "no message") so Trigger can collect the appended messages in registration
// order. A panicking handler is recovered into an error message and the
// fan-out continues — it never short-circuits.
package hooks

import (
	"fmt"
	"strings"
	"sync"
)

// Event is one hook invocation.
type Event struct {
	// Key is the fully-qualified event key, e.g. "session:auto-reset".
	Key string
	// Family is the coarse group, e.g. "session".
	Family string
	// Action is the verb, e.g. "auto-reset".
	Action string
	// Data carries arbitrary event payloads.
	Data map[string]any
}

// Handler handles an event and optionally returns a message for the caller.
// A panic is recovered by Trigger and turned into an error message.
type Handler func(Event) string

type entry struct {
	key string
	fn  Handler
}

// Registry holds hook handlers in registration order.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
	order    []entry
	enabled  bool
}

// NewRegistry creates an enabled registry.
func NewRegistry() *Registry {
	return &Registry{handlers: map[string][]Handler{}, enabled: true}
}

// On registers fn under key. Keys are matched exactly or as a family prefix
// (see match). Registration order is preserved across keys.
func (r *Registry) On(key string, fn Handler) {
	if r == nil || fn == nil || strings.TrimSpace(key) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = map[string][]Handler{}
	}
	r.handlers[key] = append(r.handlers[key], fn)
	r.order = append(r.order, entry{key: key, fn: fn})
}

// SetEnabled toggles the registry. A disabled registry's Trigger invokes
// nothing and returns nil.
func (r *Registry) SetEnabled(en bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = en
}

// Enabled reports whether Trigger fans out.
func (r *Registry) Enabled() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled
}

// match reports whether a handler registered under key fires for ev: exact
// key match, exact family match, or family-prefix match of either ("session"
// matches "session:auto-reset").
func match(key string, ev Event) bool {
	if key == "" {
		return false
	}
	if key == ev.Key || key == ev.Family {
		return true
	}
	if ev.Key != "" && strings.HasPrefix(ev.Key, key+":") {
		return true
	}
	if ev.Family != "" && ev.Family != ev.Key && strings.HasPrefix(ev.Family, key+":") {
		return true
	}
	return false
}

// Trigger fans out ev to every matching handler in registration order,
// recovers per-handler panics, and returns the appended non-empty messages.
// It never short-circuits: all matching handlers run.
func (r *Registry) Trigger(ev Event) []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	if !r.enabled {
		r.mu.RUnlock()
		return nil
	}
	type pending struct {
		key string
		fn  Handler
	}
	var fns []pending
	for _, e := range r.order {
		if match(e.key, ev) {
			fns = append(fns, pending{key: e.key, fn: e.fn})
		}
	}
	r.mu.RUnlock()

	var out []string
	for _, p := range fns {
		msg, panicked := invoke(p.fn, ev)
		if panicked {
			out = append(out, fmt.Sprintf("hook %q handler panic: %s", p.key, msg))
			continue
		}
		if msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

// invoke runs fn, recovering panics into (message, true).
func invoke(fn Handler, ev Event) (msg string, panicked bool) {
	defer func() {
		if rec := recover(); rec != nil {
			msg = fmt.Sprintf("%v", rec)
			panicked = true
		}
	}()
	return fn(ev), false
}
