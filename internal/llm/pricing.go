// Per-model token pricing and cost accounting.
//
// OpenClaw reference (read-only):
//
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/model-catalog/pricing.ts
//	  (catalog-first $/token resolution with hosted-overlay fallback)
//
// Here the catalog is a static estimate table: per-1k-token USD for input
// and output. Unknown models cost 0 (no error) so accounting never blocks
// inference. Refresh the numbers against provider pages before quoting.
package llm

import "strings"

// Table maps a (lowercased) model id to {input $/1k tokens, output $/1k
// tokens} in USD. Values are estimates, not live quotes.
var Table = map[string][2]float64{
	// OpenAI.
	"gpt-4o":       {2.50, 10.00},
	"gpt-4o-mini":  {0.15, 0.60},
	"gpt-4.1":      {2.00, 8.00},
	"gpt-4.1-mini": {0.40, 1.60},
	// Anthropic.
	"claude-sonnet": {3.00, 15.00},
	"claude-opus":   {15.00, 75.00},
	"claude-haiku":  {0.80, 4.00},
	// Google.
	"gemini-2.5-flash": {0.30, 2.50},
	"gemini-2.5-pro":   {1.25, 10.00},
	// DeepSeek.
	"deepseek-chat":     {0.27, 1.10},
	"deepseek-reasoner": {0.55, 2.19},
	// Groq-served open weights.
	"llama-3.3-70b": {0.59, 0.79},
	"qwen3-32b":     {0.20, 0.40},
	// Meta / community.
	"muse-spark": {0.00, 0.00}, // free-tier route via OpenRouter; kept explicit
	"nemotron":   {0.30, 0.90},
	// Moonshot.
	"kimi-k2": {0.60, 2.50},
}

// normalizePriceKey lowercases, trims, and strips a "provider/" routing
// prefix (e.g. "openrouter/gpt-4o-mini") plus any ":variant" suffix.
func normalizePriceKey(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.Index(m, ":"); i >= 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}

// priceFor resolves a model's rate: exact match first, then the longest
// table key that is a prefix of the model id (so dated snapshots like
// "gpt-4o-2024-08-06" or "claude-sonnet-4-5-20250929" still price).
func priceFor(model string) ([2]float64, bool) {
	m := normalizePriceKey(model)
	if m == "" {
		return [2]float64{}, false
	}
	if p, ok := Table[m]; ok {
		return p, true
	}
	best := ""
	var bestP [2]float64
	for k, p := range Table {
		if strings.HasPrefix(m, k) && len(k) > len(best) {
			best, bestP = k, p
		}
	}
	if best == "" {
		return [2]float64{}, false
	}
	return bestP, true
}

// Cost returns the estimated USD cost of usage u for model. Unknown models
// cost 0 with no error. Only input/output buckets are priced; cached and
// reasoning tokens are accounted inside those buckets by Normalize.
func Cost(model string, u Usage) float64 {
	p, ok := priceFor(model)
	if !ok {
		return 0
	}
	return float64(u.Input)/1000*p[0] + float64(u.Output)/1000*p[1]
}
