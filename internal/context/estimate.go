// Token estimation for the context budget (Phase 2).
//
// Heuristic: ~4 chars per token, plus a small per-message framing
// overhead. Deliberately conservative (overestimates) so compaction
// triggers before the provider rejects the request — an early compact
// costs a summary call, a rejected request costs the whole turn.
package context

// CharsPerToken is the estimation heuristic.
const CharsPerToken = 4

// FramingOverhead accounts for role/name/template tokens per message.
const FramingOverhead = 8

// EstimateText approximates tokens for one text blob.
func EstimateText(s string) int {
	if s == "" {
		return 0
	}
	return len(s)/CharsPerToken + 1
}

// EstimateThread sums parts plus per-part framing overhead.
func EstimateThread(parts []string) int {
	total := 0
	for _, p := range parts {
		total += EstimateText(p) + FramingOverhead
	}
	return total
}

// Budget tracks a context window with a safety reserve.
type Budget struct {
	// Window is the model's context window in tokens (0 = unknown).
	Window int
	// Reserve is kept free for the reply (0 = 10% of window).
	Reserve int
}

func (b Budget) reserve() int {
	if b.Reserve > 0 {
		return b.Reserve
	}
	if b.Window > 0 {
		return b.Window / 10
	}
	return 0
}

// Usable returns window minus reserve.
func (b Budget) Usable() int { return b.Window - b.reserve() }

// State classifies used tokens: "ok", "prune" (drop old tool outputs),
// "compact" (summarize history), or "over" (immediate compact, no new turn).
func (b Budget) State(used int) string {
	if b.Window <= 0 {
		return "ok"
	}
	u := b.Usable()
	switch {
	case used >= u:
		return "over"
	case used >= u*85/100:
		return "compact"
	case used >= u*60/100:
		return "prune"
	default:
		return "ok"
	}
}
