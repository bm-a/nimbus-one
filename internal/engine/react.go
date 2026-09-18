// Package engine implements the ReAct (OODA) agent loop.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/llm/pool"
	"nimbus-one/internal/tools"
)

// Engine runs the observe-orient-decide-act loop against an LLM provider.
// Mode is optional and seamless: "build" (default, full access) or "plan"
// (read-only — mutating tools are blocked with an explanatory message).
type Engine struct {
	LLM        llm.Provider
	Tools      *tools.Registry
	MaxSteps   int
	Mode       string
	OnProgress func(string)
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
	mode := e.CurrentMode()
	defs := registryToDefs(e.Tools)
	if mode == ModePlan {
		defs = filterReadOnlyDefs(defs)
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: "PLAN MODE: read-only. Inspect files, search, and fetch URLs. Do NOT attempt writes, shell commands, or any mutating actions — propose changes as text instead."})
	}

	lastText := ""
	overflows := 0
	for step := 0; step < e.maxSteps(); step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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
		if len(calls) == 0 {
			return text, nil
		}
		lastText = text
		e.progress(fmt.Sprintf("step %d/%d: executing %d tool call(s)", step+1, e.maxSteps(), len(calls)))
		for _, tc := range calls {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			result := e.executeWithRetries(ctx, tc, e.CurrentMode())
			msgs = append(msgs, llm.Message{
				Role:       llm.RoleTool,
				Content:    result,
				Name:       tc.Name,
				ToolCallID: tc.ID,
			})
		}
	}
	if lastText != "" {
		return "", fmt.Errorf("engine: max steps %d exceeded (last assistant text: %.200s)", e.maxSteps(), lastText)
	}
	return "", fmt.Errorf("engine: max steps %d exceeded", e.maxSteps())
}

// executeWithRetries parses JSON args and executes a tool up to 3 times,
// returning the success output or an error-feedback string for the model.
// In plan mode, mutating tools are blocked without executing.
func (e *Engine) executeWithRetries(ctx context.Context, tc llm.ToolCall, mode string) string {
	if mode == ModePlan && !IsReadOnlyTool(tc.Name) {
		return fmt.Sprintf("tool %q blocked: plan mode is read-only — describe the proposed change as text instead of executing it", tc.Name)
	}
	if e.Tools == nil {
		return fmt.Sprintf("tool %q failed: no tool registry configured", tc.Name)
	}
	tool, ok := e.Tools.Get(tc.Name)
	if !ok {
		return fmt.Sprintf("tool %q failed: unknown tool (available: %s)", tc.Name, strings.Join(e.Tools.Names(), ", "))
	}
	var args map[string]any
	if strings.TrimSpace(tc.Arguments) == "" {
		args = map[string]any{}
	} else if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return fmt.Sprintf("tool %q failed: invalid JSON arguments: %v (arguments were: %s)", tc.Name, err, tc.Arguments)
	}
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
