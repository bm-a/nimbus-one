// Model context-window catalog and guard.
//
// OpenClaw reference (read-only):
//
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/agents/model-context-window.ts
//	  (catalog-declared windows)
//	/data/data/com.termux/files/home/tmp/openclaw-src/src/agents/context-window-guard.ts
//	  (warn < max(8k, 20%) remaining, block < max(4k, 10%) remaining)
package llm

import "strings"

// DefaultWindow is used when a model is absent from Catalog.
const DefaultWindow = 128000

// Catalog maps a (lowercased) model id or family prefix to its context
// window in tokens. Values are estimates from provider docs.
var Catalog = map[string]int{
	"gpt-4o":            128000,
	"gpt-4o-mini":       128000,
	"gpt-4.1":           1047576,
	"gpt-4.1-mini":      1047576,
	"o1":                200000,
	"o3":                200000,
	"claude-sonnet":     200000,
	"claude-opus":       200000,
	"claude-haiku":      200000,
	"gemini-2.5-flash":  1048576,
	"gemini-2.5-pro":    1048576,
	"deepseek-chat":     128000,
	"deepseek-reasoner": 64000,
	"llama-3.3-70b":     128000,
	"qwen3-32b":         131072,
	"muse-spark":        1000000,
	"nemotron":          128000,
	"kimi-k2":           256000,
}

// normalizeWindowKey lowercases and strips routing prefixes/suffixes the
// same way normalizePriceKey does, so both tables accept identical input.
func normalizeWindowKey(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	if i := strings.Index(m, ":"); i >= 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}

// WindowFor resolves a model's context window: exact catalog hit, then the
// longest family-prefix hit (dated snapshots resolve through their family),
// else DefaultWindow.
func WindowFor(model string) int {
	m := normalizeWindowKey(model)
	if m == "" {
		return DefaultWindow
	}
	if w, ok := Catalog[m]; ok {
		return w
	}
	best := ""
	bestW := 0
	for k, w := range Catalog {
		if strings.HasPrefix(m, k) && len(k) > len(best) {
			best, bestW = k, w
		}
	}
	if best == "" {
		return DefaultWindow
	}
	return bestW
}

// Guard evaluates remaining context: "ok", "warn" (remaining below
// max(8k, 20% of window)), or "block" (remaining below max(4k, 10%)).
// Non-positive windows fall back to DefaultWindow; negative usage clamps
// to zero.
func Guard(used, window int) string {
	if window <= 0 {
		window = DefaultWindow
	}
	if used < 0 {
		used = 0
	}
	remaining := window - used
	warnBelow := 8000
	if q := window / 5; q > warnBelow {
		warnBelow = q
	}
	blockBelow := 4000
	if q := window / 10; q > blockBelow {
		blockBelow = q
	}
	switch {
	case remaining < blockBelow:
		return "block"
	case remaining < warnBelow:
		return "warn"
	default:
		return "ok"
	}
}
