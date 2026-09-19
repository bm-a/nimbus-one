// Package engine — three-stage context compaction (Phase 2).
//
// Budget states (internal/context): ok → nothing; prune → shrink old tool
// outputs in place; compact/over → summarize dropped history with a
// dedicated summary call, falling back to the extractive compactor when
// the model is unavailable. The newest turns are always protected; the
// system prompt is never dropped.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	nctx "nimbus-one/internal/context"
	"nimbus-one/internal/llm"
	"nimbus-one/internal/tools"
)

// Protection window: newest N messages are never compacted.
const compactKeepLast = 8

// Prune limits.
const (
	pruneKeepRecent   = 6
	pruneMaxToolBytes = 2000
)

// SummaryInstructions forces identifier preservation: the failure mode of
// naive summarization is losing file paths, IDs, and decisions.
const SummaryInstructions = `Summarize this agent turn history for continuation. ` +
	` Preserve VERBATIM: file paths, identifiers, URLs, error messages, numbers, ` +
	`and any user-stated constraints. Structure: (1) current goal, ` +
	`(2) decisions made + why, (3) open files and their state, ` +
	`(4) pending next step. Be dense; omit greetings and chatter.`

// estimateUsage approximates prompt tokens for messages + tool defs.
func estimateUsage(msgs []llm.Message, defs []llm.ToolDef) int {
	parts := make([]string, 0, len(msgs)+1)
	for _, m := range msgs {
		parts = append(parts, m.Role, m.Content, m.Name)
		if len(m.ToolCalls) > 0 {
			if b, err := json.Marshal(m.ToolCalls); err == nil {
				parts = append(parts, string(b))
			}
		}
	}
	for _, d := range defs {
		parts = append(parts, d.Name, d.Description)
		if b, err := json.Marshal(d.Parameters); err == nil {
			parts = append(parts, string(b))
		}
	}
	return nctx.EstimateThread(parts)
}

// budgetWindow resolves the context window: explicit BudgetWindow wins,
// else the model catalog, else the default.
func (e *Engine) budgetWindow() int {
	if e.BudgetWindow > 0 {
		return e.BudgetWindow
	}
	return llm.WindowFor(e.ModelID)
}

// PruneOldToolOutputs shrinks tool results older than the newest
// keepRecent messages to a head pointer. Returns a copy; input untouched.
func PruneOldToolOutputs(msgs []llm.Message, keepRecent, maxBytes int) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	if keepRecent <= 0 {
		keepRecent = pruneKeepRecent
	}
	if maxBytes <= 0 {
		maxBytes = pruneMaxToolBytes
	}
	res := make([]llm.Message, len(msgs))
	copy(res, msgs)
	cutoff := len(res) - keepRecent
	if cutoff < 0 {
		cutoff = 0
	}
	for i := 0; i < cutoff; i++ {
		m := &res[i]
		if m.Role != llm.RoleTool || len(m.Content) <= maxBytes {
			continue
		}
		head := m.Content
		if idx := strings.Index(head, "\n"); idx >= 0 && idx < 200 {
			head = head[:idx]
		}
		if len(head) > 200 {
			head = head[:200]
		}
		m.Content = fmt.Sprintf("[pruned: was %d bytes, kept head: %s — full result discarded with history]",
			len(m.Content), strings.TrimSpace(head))
	}
	return res
}

// SummarizeDropped asks the model to compress dropped turns. Tools: none
// (empty registry + plan mode); on provider error the caller falls back.
func SummarizeDropped(ctx context.Context, provider llm.Provider, model string, dropped []llm.Message) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("compact: nil provider")
	}
	var b strings.Builder
	for _, m := range dropped {
		label := m.Role
		if m.Name != "" {
			label += "/" + m.Name
		}
		snip := m.Content
		if len(snip) > 2000 {
			snip = snip[:2000] + "…[cut]"
		}
		b.WriteString("[" + label + "]\n" + snip + "\n\n")
	}
	child := &Engine{
		LLM:      provider,
		Tools:    tools.NewRegistry(),
		MaxSteps: 2,
		Mode:     ModePlan,
		ModelID:  model,
	}
	return child.Run(ctx, SummaryInstructions, "History to summarize:\n\n"+b.String())
}

// maybeCompact applies one budget stage before a turn. It returns the
// (possibly new) message slice.
func (e *Engine) maybeCompact(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) []llm.Message {
	budget := nctx.Budget{Window: e.budgetWindow()}
	switch budget.State(estimateUsage(msgs, defs)) {
	case "prune":
		e.progress("context: pruning old tool outputs")
		out := PruneOldToolOutputs(msgs, pruneKeepRecent, pruneMaxToolBytes)
		e.persist(llm.RoleSystem, "[context: pruned old tool outputs]", "", "")
		return out
	case "compact", "over":
		return e.summaryCompact(ctx, msgs)
	default:
		return msgs
	}
}

// summaryCompact replaces everything but the system prompt + newest turns
// with a model summary, degrading to extractive compaction on failure.
func (e *Engine) summaryCompact(ctx context.Context, msgs []llm.Message) []llm.Message {
	if len(msgs) <= compactKeepLast+1 {
		return msgs
	}
	var system *llm.Message
	rest := msgs
	if len(rest) > 0 && rest[0].Role == llm.RoleSystem {
		s := rest[0]
		system = &s
		rest = rest[1:]
	}
	kept := rest
	dropped := []llm.Message{}
	if len(rest) > compactKeepLast {
		dropped = rest[:len(rest)-compactKeepLast]
		kept = rest[len(rest)-compactKeepLast:]
	}
	if len(dropped) == 0 {
		return msgs
	}
	summary, err := SummarizeDropped(ctx, e.LLM, e.ModelID, dropped)
	if err != nil {
		e.progress(fmt.Sprintf("context: summary failed (%v) — extractive fallback", err))
		out := compactOldest(msgs, 30)
		e.persist(llm.RoleSystem, "[context: extractive compact after summary failure]", "", "")
		return out
	}
	e.progress("context: summarized dropped history")
	out := make([]llm.Message, 0, 2+len(kept))
	if system != nil {
		out = append(out, *system)
	}
	note := fmt.Sprintf("[conversation summary — %d turns compressed — identifiers preserved]\n%s", len(dropped), summary)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: note})
	out = append(out, kept...)
	e.persist(llm.RoleSystem, note, "", "")
	return out
}
