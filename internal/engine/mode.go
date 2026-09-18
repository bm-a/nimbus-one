// Package engine — plan/build mode enforcement.
//
// Plan mode is read-only: the agent can inspect (read/list/search/fetch) but
// any mutating capability (bash, write, skill scripts, MCP writes) is blocked
// with a clear message instead of executing. Build mode is full access.
// Mode is seamless and optional: default is build; pass --mode plan, send
// /mode plan in REPL, or {"mode":"plan"} to the HTTP API.
package engine

import (
	"strings"
	"sync"

	"nimbus-one/internal/llm"
)

// Mode constants.
const (
	ModePlan  = "plan"
	ModeBuild = "build"
)

// readOnlyTools are safe in plan mode. Everything else is treated as mutating
// and blocked unless mode == build.
var readOnlyTools = map[string]bool{
	"read":       true,
	"list":       true,
	"search":     true,
	"web_fetch":  true,
	"web_search": true,
}

// NormalizeMode returns ModeBuild for any unknown value (seamless default).
func NormalizeMode(m string) string {
	if m == ModePlan {
		return ModePlan
	}
	return ModeBuild
}

// IsReadOnlyTool reports whether a tool may run in plan mode.
func IsReadOnlyTool(name string) bool { return readOnlyTools[name] }

// modeMu guards process-wide mode switches (e.g. REPL /mode) so concurrent
// runs observe a consistent value.
var modeMu sync.RWMutex

// SetMode switches the engine process-wide ("plan" or "build").
// Unknown values normalize to build.
func (e *Engine) SetMode(m string) {
	modeMu.Lock()
	defer modeMu.Unlock()
	e.Mode = NormalizeMode(m)
}

// CurrentMode returns the active process-wide mode.
func (e *Engine) CurrentMode() string {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return NormalizeMode(e.Mode)
}

// filterReadOnlyDefs hides mutating tools from the model in plan mode so it
// plans with words instead of attempting blocked calls.
func filterReadOnlyDefs(defs []llm.ToolDef) []llm.ToolDef {
	out := defs[:0:0]
	for _, d := range defs {
		if IsReadOnlyTool(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// compactOldest drops the oldest pct% of non-system history and inserts an
// extractive summary marker so the model keeps causal continuity.
func compactOldest(msgs []llm.Message, pct int) []llm.Message {
	if len(msgs) <= 2 || pct <= 0 || pct >= 100 {
		return msgs
	}
	head := msgs[:0:0]
	var rest []llm.Message
	if msgs[0].Role == llm.RoleSystem {
		head = append(head, msgs[0])
		rest = msgs[1:]
	} else {
		rest = msgs
	}
	cut := len(rest) * pct / 100
	if cut < 1 {
		cut = 1
	}
	if cut >= len(rest) {
		cut = len(rest) - 1
	}
	dropped := rest[:cut]
	kept := rest[cut:]
	var sb strings.Builder
	sb.WriteString("[...auto-compact: oldest " + itoa(cut) + " turns summarized...] ")
	for _, m := range dropped {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		snip := strings.TrimSpace(m.Content)
		if len(snip) > 160 {
			snip = snip[:160] + "…"
		}
		sb.WriteString("[" + m.Role + "] " + snip + " ")
		if sb.Len() > 1200 {
			break
		}
	}
	note := llm.Message{Role: llm.RoleSystem, Content: strings.TrimSpace(sb.String())}
	return append(append(head, note), kept...)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
