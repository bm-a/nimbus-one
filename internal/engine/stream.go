// Package engine — streaming runs.
//
// The provider contract (internal/llm/types.go) already exposes Chat chunks
// (Delta text + ToolCalls); RunStream drives the same ReAct loop as Run but
// forwards each text delta to emit as it arrives and re-streams the next turn
// after executing tool calls. Providers without streaming degrade to a full
// Run plus a single terminal emit.
package engine

import (
	"context"
	"fmt"
	"strings"

	"nimbus-one/internal/llm"
	"nimbus-one/internal/session"
)

// RunStream executes the ReAct loop like Run, forwarding provider Chat deltas
// to emit as they arrive. Tool calls are executed between turns (via the same
// retry path as Run) and the loop re-streams the follow-up turn. When the
// provider's Chat is unavailable, it degrades to Run plus one terminal emit.
// It returns the final assistant text. A nil emit disables forwarding.
func (e *Engine) RunStream(ctx context.Context, system, user string, emit func(llm.Chunk)) (string, error) {
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
	defs := registryToDefs(e.Tools)
	defs = filterDefsByRuleset(defs, e.effectiveRuleset())
	defs = filterDefsByShape(defs, shapeFor(e.ModelID))
	if e.Ruleset == nil && e.CurrentMode() == ModePlan {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: "PLAN MODE: read-only. Inspect files, search, and fetch URLs. Do NOT attempt writes, shell commands, or any mutating actions — propose changes as text instead."})
	}

	emitChunk := func(ck llm.Chunk) {
		if emit != nil {
			emit(ck)
		}
	}

	lastText := ""
	for step := 0; step < e.maxSteps(); step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		msgs = e.maybeCompact(ctx, msgs, defs)
		e.progress(fmt.Sprintf("step %d/%d: streaming model", step+1, e.maxSteps()))
		ch, err := e.LLM.Chat(ctx, llm.ChatRequest{Messages: msgs, Tools: defs, Stream: true})
		if err != nil {
			if step == 0 {
				// Provider lacks streaming: degrade to a full Run
				// plus a single terminal emit.
				out, runErr := e.Run(ctx, system, user)
				if runErr != nil {
					return "", runErr
				}
				emitChunk(llm.Chunk{Delta: out, Done: true})
				return out, nil
			}
			// Mid-loop streaming failure: fall back to Complete for
			// this turn (Complete-equivalent) and continue the loop.
			text, calls, cerr := e.LLM.Complete(ctx, llm.ChatRequest{Messages: msgs, Tools: defs})
			if cerr != nil {
				return "", fmt.Errorf("engine: step %d llm call: %w", step+1, cerr)
			}
			emitChunk(llm.Chunk{Delta: text, ToolCalls: calls, Done: true})
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
				msgs = append(msgs, llm.Message{Role: llm.RoleTool, Content: result, Name: tc.Name, ToolCallID: tc.ID})
				e.persist(session.RoleTool, result, tc.Name, tc.ID)
				e.fireHook("tool.after", "tool", "after", map[string]any{"tool": tc.Name, "ok": !isToolFailure(result)})
			}
			continue
		}
		var sb strings.Builder
		var calls []llm.ToolCall
		failed := false
		for ck := range ch {
			if ck.Err != nil {
				return "", fmt.Errorf("engine: step %d stream: %w", step+1, ck.Err)
			}
			if ck.Delta != "" {
				sb.WriteString(ck.Delta)
				emitChunk(llm.Chunk{Delta: ck.Delta})
			}
			if len(ck.ToolCalls) > 0 {
				calls = append(calls, ck.ToolCalls...)
			}
			if ctx.Err() != nil {
				failed = true
				break
			}
		}
		if failed {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		text := sb.String()
		emitChunk(llm.Chunk{ToolCalls: calls, Done: true})
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
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, Content: result, Name: tc.Name, ToolCallID: tc.ID})
			e.persist(session.RoleTool, result, tc.Name, tc.ID)
			e.fireHook("tool.after", "tool", "after", map[string]any{"tool": tc.Name, "ok": !isToolFailure(result)})
		}
	}
	if lastText != "" {
		return "", fmt.Errorf("engine: max steps %d exceeded (last assistant text: %.200s)", e.maxSteps(), lastText)
	}
	return "", fmt.Errorf("engine: max steps %d exceeded", e.maxSteps())
}
