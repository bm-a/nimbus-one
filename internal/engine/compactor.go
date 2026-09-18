// Package engine context compaction helpers.
package engine

import (
	"fmt"
	"strings"

	"nimbus-one/internal/llm"
)

// Compactor decides when history must shrink and performs extractive
// summarization to keep the prompt within budget.
type Compactor struct {
	Limit   int
	Reserve float64
}

// EstimateTokens approximates token count at ~4 chars per token.
func (c *Compactor) EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(s) / 4
}

// ShouldCompact reports whether used tokens reach 80% of the limit.
func (c *Compactor) ShouldCompact(used int) bool {
	if c == nil || c.Limit <= 0 {
		return false
	}
	return used >= int(float64(c.Limit)*0.8)
}

// Compact keeps msgs[0] when it is a system message, inserts a rolling
// extractive summary of the dropped middle, and appends the last keepLast
// messages.
func (c *Compactor) Compact(msgs []llm.Message, keepLast int) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	if keepLast <= 0 {
		keepLast = 1
	}
	hasSystem := msgs[0].Role == llm.RoleSystem
	head := 0
	var system *llm.Message
	if hasSystem {
		s := msgs[0]
		system = &s
		head = 1
	}
	rest := msgs[head:]
	if len(rest) <= keepLast {
		out := make([]llm.Message, len(msgs))
		copy(out, msgs)
		return out
	}
	dropped := rest[:len(rest)-keepLast]
	kept := rest[len(rest)-keepLast:]

	var sb strings.Builder
	for _, m := range dropped {
		sb.WriteString("[" + m.Role + "] " + m.Content + "\n")
	}
	full := sb.String()
	excerpt := full
	if len(excerpt) > 500 {
		excerpt = excerpt[:500]
	}
	summary := fmt.Sprintf("Earlier conversation summary: %s [...%d turns summarized...]", strings.TrimSpace(excerpt), len(dropped))

	out := make([]llm.Message, 0, 2+len(kept))
	if system != nil {
		out = append(out, *system)
	}
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: summary})
	out = append(out, kept...)
	return out
}

// BuildPrompt composes the internal prompt: the sole source of the agent's
// described capabilities.
func BuildPrompt(soul, user, memory string, facts []string, skillCaps string) string {
	var sb strings.Builder
	if strings.TrimSpace(soul) != "" {
		sb.WriteString(strings.TrimSpace(soul))
	}
	if strings.TrimSpace(user) != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("User context:\n" + strings.TrimSpace(user))
	}
	if strings.TrimSpace(memory) != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Memory:\n" + strings.TrimSpace(memory))
	}
	cleanFacts := make([]string, 0, len(facts))
	for _, f := range facts {
		if strings.TrimSpace(f) != "" {
			cleanFacts = append(cleanFacts, "- "+strings.TrimSpace(f))
		}
	}
	if len(cleanFacts) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Known facts:\n" + strings.Join(cleanFacts, "\n"))
	}
	if strings.TrimSpace(skillCaps) != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Capabilities (authoritative — do not claim others):\n" + strings.TrimSpace(skillCaps))
	}
	return sb.String()
}
