// Package engine implements the ReAct (OODA) agent loop.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"nimbus-one/internal/hooks"
	"nimbus-one/internal/llm"
	"nimbus-one/internal/llm/pool"
	"nimbus-one/internal/perms"
	"nimbus-one/internal/session"
	"nimbus-one/internal/tools"
)

// Engine runs the observe-orient-decide-act loop against an LLM provider.
// Mode is optional and seamless: "build" (default, full access) or "plan"
// (read-only — mutating tools are blocked with an explanatory message).
//
// Ruleset (Phase 0) overrides the mode preset when set: tool visibility and
// execution are decided by perms.Evaluate. Ask verdicts consult Ask (nil =
// headless = deny with guidance). Approvals accumulates session always-allows.
// When Session+SessionID are set, every turn part is persisted best-effort.
type Engine struct {
	LLM        llm.Provider
	Tools      *tools.Registry
	MaxSteps   int
	Mode       string
	OnProgress func(string)
	Ruleset    perms.Ruleset
	Ask        perms.AskFunc
	Approvals  *perms.Approvals
	Session    *session.Store
	SessionID  string
	// ModelID drives per-model tool shaping via the profile table
	// (e.g. GPT-family models get apply_patch instead of edit/write).
	// Empty means the default profile.
	ModelID string
	// BudgetWindow overrides the context window for compaction (0 = auto
	// from the model catalog). Tests set a tiny window to force stages.
	BudgetWindow int
	// Hooks receives lifecycle events (session.start, tool.after). Nil =
	// no listeners. Handler output goes to progress only — hooks observe,
	// they never inject loop input in V1.
	Hooks *hooks.Registry
}

func (e *Engine) maxSteps() int {
	if e.MaxSteps <= 0 {
		return 10
	}
	return e.MaxSteps
}

func (e *Engine) progress(msg string) {
	if e.OnProgress != nil {
		e.OnProgress(msg)
	}
}

// registryToDefs converts registered tools to LLM tool definitions.
func registryToDefs(r *tools.Registry) []llm.ToolDef {
	if r == nil {
		return nil
	}
	all := r.All()
	out := make([]llm.ToolDef, 0, len(all))
	for _, t := range all {
		props := map[string]any{}
		var required []string
		for pname, p := range t.Parameters() {
			ptype := p.Type
			if ptype == "" {
				ptype = "string"
			}
			props[pname] = map[string]any{
				"type":        ptype,
				"description": p.Description,
			}
			if p.Required {
				required = append(required, pname)
			}
		}
		params := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			params["required"] = required
		}
		out = append(out, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  params,
		})
	}
	return out
}

// Run executes the ReAct loop: prompt the model, run requested tools with up
// to 3 attempts each, feed results back, and repeat until the model answers
// directly or MaxSteps is exhausted.
func (e *Engine) Run(ctx context.Context, system, user string) (string, error) {
	if e.LLM == nil {
		return "", fmt.Errorf("engine: nil LLM provider")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	msgs := []llm.Message{}
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: system})
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: user})
	e.persist(session.RoleUser, user, "", "")
	mode := e.CurrentMode()
	e.fireHook("session.start", "session", "start", map[string]any{"model": e.ModelID, "mode": mode})
	defs := registryToDefs(e.Tools)
	defs = filterDefsByRuleset(defs, e.effectiveRuleset())
	defs = filterDefsByShape(defs, shapeFor(e.ModelID))
	if e.Ruleset == nil && mode == ModePlan {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: "PLAN MODE: read-only. Inspect files, search, and fetch URLs. Do NOT attempt writes, shell commands, or any mutating actions — propose changes as text instead."})
	}

	lastText := ""
	overflows := 0
	for step := 0; step < e.maxSteps(); step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		msgs = e.maybeCompact(ctx, msgs, defs)
		e.progress(fmt.Sprintf("step %d/%d: querying model", step+1, e.maxSteps()))
		text, calls, err := e.LLM.Complete(ctx, llm.ChatRequest{
			Messages: msgs,
			Tools:    defs,
		})
		if err != nil {
			// Deterministic self-heal: on context overflow, compact the
			// oldest 30% of history and resend immediately (max 2 heals).
			var ovf *pool.ContextOverflowError
			if errors.As(err, &ovf) && overflows < 2 && len(msgs) > 2 {
				overflows++
				msgs = compactOldest(msgs, 30)
				e.progress("context overflow: compacted oldest 30%, resending")
				step-- // retry the same step
				continue
			}
			return "", fmt.Errorf("engine: step %d llm call: %w", step+1, err)
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: text, ToolCalls: calls})
		e.persist(session.RoleAssistant, text, "", "")
		if len(calls) == 0 {
			return text, nil
		}
		lastText = text
		e.progress(fmt.Sprintf("step %d/%d: executing %d tool call(s)", step+1, e.maxSteps(), len(calls)))
		for _, tc := range calls {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			result := e.executeWithRetries(ctx, tc)
			msgs = append(msgs, llm.Message{
				Role:       llm.RoleTool,
				Content:    result,
				Name:       tc.Name,
				ToolCallID: tc.ID,
			})
			e.persist(session.RoleTool, result, tc.Name, tc.ID)
			e.fireHook("tool.after", "tool", "after", map[string]any{"tool": tc.Name, "ok": !isToolFailure(result)})
		}
	}
	if lastText != "" {
		return "", fmt.Errorf("engine: max steps %d exceeded (last assistant text: %.200s)", e.maxSteps(), lastText)
	}
	return "", fmt.Errorf("engine: max steps %d exceeded", e.maxSteps())
}

// executeWithRetries parses JSON args and executes a tool up to 3 times,
// returning the success output or an error-feedback string for the model.
// Permission verdicts (deny/ask) are enforced before any execution.
func (e *Engine) executeWithRetries(ctx context.Context, tc llm.ToolCall) string {
	act, target, args, argErr := e.checkCall(tc)
	if argErr != nil {
		return fmt.Sprintf("tool %q failed: invalid JSON arguments: %v (arguments were: %s)", tc.Name, argErr, tc.Arguments)
	}
	switch act {
	case perms.Deny:
		return e.blockedMessage(tc.Name, target, act)
	case perms.Ask:
		if !e.resolveAsk(tc.Name, target) {
			return e.blockedMessage(tc.Name, target, act)
		}
	}
	if e.Tools == nil {
		return fmt.Sprintf("tool %q failed: no tool registry configured", tc.Name)
	}
	tool, ok := e.Tools.Get(tc.Name)
	if !ok {
		return fmt.Sprintf("tool %q failed: unknown tool (available: %s)", tc.Name, strings.Join(e.Tools.Names(), ", "))
	}
	debugf("tool call %s(%s)", tc.Name, tc.Arguments)
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return fmt.Sprintf("tool %q cancelled: %v", tc.Name, ctx.Err())
		}
		out, err := tool.Execute(ctx, args)
		if err == nil {
			return out
		}
		lastErr = err
	}
	return fmt.Sprintf("tool %q failed after 3 attempts: %v", tc.Name, lastErr)
}
