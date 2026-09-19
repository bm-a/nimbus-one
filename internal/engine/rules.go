// Package engine — permission-ruleset bridge (Phase 0).
//
// Modes are now presets over perms.Ruleset: build = allow-all, plan =
// deny-all-except-readonly. An explicit Engine.Ruleset overrides the mode
// preset; user rules appended after the preset always win (last-match-wins).
// Denied tools are hidden from tool definitions; ask-verdicts go through
// Engine.Ask (nil = headless = fail closed with an explanatory message).
package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"nimbus-one/internal/hooks"
	"nimbus-one/internal/llm"
	"nimbus-one/internal/perms"
)

// approvalsMu guards lazy approval-store init.
var approvalsMu sync.Mutex

// readOnlyNames returns the plan-mode tool allowlist, sorted.
func readOnlyNames() []string {
	out := make([]string, 0, len(readOnlyTools))
	for n := range readOnlyTools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// effectiveRuleset returns the explicit ruleset when set, else the mode
// preset. Custom rules must be appended AFTER the preset so they win.
func (e *Engine) effectiveRuleset() perms.Ruleset {
	if e == nil || e.Ruleset == nil {
		if e != nil && e.CurrentMode() == ModePlan {
			return perms.Plan(readOnlyNames())
		}
		return perms.Build()
	}
	return e.Ruleset
}

// filterDefsByRuleset hides blanket-denied tools from the model. Rules that
// deny only specific patterns (e.g. read *.env) do NOT hide the tool —
// the call-time check enforces those.
func filterDefsByRuleset(defs []llm.ToolDef, rs perms.Ruleset) []llm.ToolDef {
	out := defs[:0:0]
	for _, d := range defs {
		if perms.Evaluate(d.Name, "*", rs) == perms.Deny {
			continue
		}
		out = append(out, d)
	}
	return out
}

// filterDefsByShape applies the model profile's tool shape: the patch
// shape hides edit+write (the model gets apply_patch instead); the edit
// shape hides apply_patch. Unknown shapes change nothing.
func filterDefsByShape(defs []llm.ToolDef, shape string) []llm.ToolDef {
	if !llm.ShapeHidesEdit(shape) && shape != llm.ShapeEdit {
		return defs
	}
	out := defs[:0:0]
	for _, d := range defs {
		if llm.ShapeHidesEdit(shape) {
			if d.Name == "edit" || d.Name == "write" {
				continue
			}
		} else if d.Name == "apply_patch" {
			continue
		}
		out = append(out, d)
	}
	return out
}

// shapeFor resolves the tool shape for a model ID via the profile table.
func shapeFor(modelID string) string {
	return llm.MatchModelProfile(modelID).ToolShape
}

// approvals returns the session approval store, creating it on first use.
func (e *Engine) approvals() *perms.Approvals {
	approvalsMu.Lock()
	defer approvalsMu.Unlock()
	if e.Approvals == nil {
		e.Approvals = &perms.Approvals{}
	}
	return e.Approvals
}

// checkCall evaluates a tool call against ruleset + approvals and parses
// its JSON arguments for target derivation. Malformed arguments are
// reported (not silently dropped) so the model gets actionable feedback.
func (e *Engine) checkCall(tc llm.ToolCall) (perms.Action, string, map[string]any, error) {
	var args map[string]any
	var argErr error
	if strings.TrimSpace(tc.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			argErr = err
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	target := perms.TargetFor(tc.Name, args)
	act := perms.Evaluate(tc.Name, target, e.effectiveRuleset(), e.approvals().AsRuleset())
	return act, target, args, argErr
}

// resolveAsk runs the approval handler. Nil handler = headless = deny,
// with a message telling the user how to pre-approve. An approval records
// an always-allow for the exact target in this session.
func (e *Engine) resolveAsk(tool, target string) bool {
	if e.Ask == nil {
		return false
	}
	if e.Ask(tool, target) {
		e.approvals().Approve(tool, target)
		return true
	}
	return false
}

// blockedMessage explains a deny/ask-fail in model-actionable terms.
func (e *Engine) blockedMessage(tool, target string, act perms.Action) string {
	if e.CurrentMode() == ModePlan {
		return fmt.Sprintf("tool %q blocked: plan mode is read-only — describe the proposed change as text instead of executing it", tool)
	}
	if act == perms.Ask {
		return fmt.Sprintf("tool %q blocked: %q requires approval (no approval handler configured) — proceed with what you can do without it, or ask the user to approve", tool, target)
	}
	return fmt.Sprintf("tool %q blocked by permission policy for %q — proceed without it", tool, target)
}

// fireHook triggers a lifecycle event; handler messages go to progress.
// Nil registry is a no-op. Hooks observe the loop — they cannot inject
// input or veto calls in V1 (that would make them a second permission
// system shadowing perms).
func (e *Engine) fireHook(key, family, action string, data map[string]any) {
	if e.Hooks == nil {
		return
	}
	for _, msg := range e.Hooks.Trigger(hooks.Event{Key: key, Family: family, Action: action, Data: data}) {
		e.progress(fmt.Sprintf("hook %s: %s", key, msg))
	}
}

// isToolFailure reports the executeWithRetries error-feedback shape.
func isToolFailure(result string) bool {
	return strings.HasPrefix(result, "tool \"") &&
		(strings.Contains(result, " failed") || strings.Contains(result, " blocked") || strings.Contains(result, " cancelled"))
}

// persist records a turn part in the attached session store. Nil store =
// no-op. Store errors are surfaced via progress (never silent) but never
// fail the run — the agent's answer matters more than the archive.
func (e *Engine) persist(role, content, name, toolCallID string) {
	if e.Session == nil || e.SessionID == "" {
		return
	}
	if _, err := e.Session.AppendMessage(e.SessionID, role, content, name, toolCallID); err != nil {
		e.progress(fmt.Sprintf("warning: session persist failed: %v", err))
	}
}
