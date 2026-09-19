// Package engine — subagent context modes.
//
// Mirrors OpenClaw (read-only reference in /data/.../tmp/openclaw-src):
//   - src/agents/subagents/spawn/subagent-spawn-context.ts (isolated default
//     vs fork same-agent, lightContext with no transcript)
//   - src/agents/tools/sessions-spawn-tool.ts (spawn schema)
//
// A child run is either isolated (fresh scratchpad, default), fork (continues
// the parent transcript — only valid for the same agent), or light (no
// transcript at all, brief-only).
package engine

import (
	"strings"

	"nimbus-one/internal/llm"
)

// Child context modes.
const (
	ChildIsolated = "isolated"
	ChildFork     = "fork"
	ChildLight    = "light"
)

// ChildSpec describes how a subagent run relates to its parent.
type ChildSpec struct {
	Mode string // isolated | fork | light ("" means "inherit parentMode")
	// Transcript is the parent history a forked child continues.
	Transcript []llm.Message
	// Tools optionally restricts the child's tool allowlist (nil = inherit all).
	Tools []string
}

// Resolve normalizes the spec: empty mode inherits parentMode (default
// isolated); a fork requested for a different agent degrades to isolated
// with an explanatory note; light drops any transcript. It returns the
// resolved spec and a note ("" when nothing was adjusted).
func (c ChildSpec) Resolve(parentMode string, sameAgent bool) (ChildSpec, string) {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(parentMode))
		if mode == "" {
			mode = ChildIsolated
		}
	}
	out := c
	var notes []string
	switch mode {
	case ChildFork:
		if !sameAgent {
			out.Mode = ChildIsolated
			notes = append(notes, "fork requested for a different agent: fell back to isolated (fork requires same-agent)")
		} else {
			out.Mode = ChildFork
		}
	case ChildLight:
		out.Mode = ChildLight
		if len(out.Transcript) > 0 {
			out.Transcript = nil
			notes = append(notes, "light context: parent transcript dropped (brief-only)")
		}
	case ChildIsolated:
		out.Mode = ChildIsolated
	default:
		out.Mode = ChildIsolated
		notes = append(notes, "unknown context mode "+quote(c.Mode)+": fell back to isolated")
	}
	return out, strings.Join(notes, "; ")
}

func quote(s string) string { return "\"" + s + "\"" }

// ForkTranscript returns the last keepLast messages of msgs (copied) for a
// forked child. keepLast <= 0 or >= len(msgs) returns a full copy.
func ForkTranscript(msgs []llm.Message, keepLast int) []llm.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	if keepLast > 0 && keepLast < len(out) {
		return out[len(out)-keepLast:]
	}
	return out
}
