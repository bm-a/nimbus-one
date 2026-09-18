// Package doctor — knowledge base.
//
// Knowledge is the built-in fix-it database: every known failure maps to a
// cause and a concrete fix. It powers `nimbus-one fix <symptom>`, enriches
// `nimbus-one doctor` output, and is mirrored in docs/TROUBLESHOOTING.md for AI
// coding agents (OpenCode, Nimbus One itself) that work in this repo.
package doctor

import (
	"sort"
	"strings"
)

// Entry is one known failure and its fix.
type Entry struct {
	ID       string   // stable id, e.g. "llm-401"
	Keywords []string // matched against user queries
	Title    string
	Cause    string
	Fix      string
	Commands []string // exact commands to try, in order
}

// Knowledge is the full database, ordered by frequency.
var Knowledge = []Entry{
	{
		ID:       "llm-401",
		Keywords: []string{"401", "unauthorized", "invalid key", "rejected", "forbidden key", "bad key"},
		Title:    "Provider rejects the API key (401/403)",
		Cause:    "The key is wrong, revoked, or pasted with extra whitespace. Nimbus One marks the key Dead and never retries it silently.",
		Fix:      "Replace the key. The wizard validates live before saving.",
		Commands: []string{"nimbus-one config", "nimbus-one secrets set openrouter <key>", "nimbus-one run \"hello\""},
	},
	{
		ID:       "llm-429",
		Keywords: []string{"429", "rate limit", "rate-limited", "too many requests", "cooling"},
		Title:    "Rate limited (429) — keys cooling down",
		Cause:    "Free tiers throttle aggressively. Nimbus One already moved traffic to the next key and the hot key rejoins automatically after Retry-After.",
		Fix:      "Wait, or add a second key for the same provider so the pool has somewhere to go. Spreading models across providers helps more than retrying.",
		Commands: []string{"nimbus-one dashboard", "nimbus-one secrets set openrouter <second-key>", "nimbus-one config"},
	},
	{
		ID:       "llm-5xx",
		Keywords: []string{"500", "502", "503", "504", "outage", "server error", "service unavailable", "overloaded"},
		Title:    "Provider outage (5xx)",
		Cause:    "The provider side is failing. Nimbus One trips the circuit breaker after 3 consecutive failures and fails over to your next fallback tier.",
		Fix:      "Check the provider status page. If prolonged, reorder fallbacks so a healthy provider is first.",
		Commands: []string{"nimbus-one status", "nimbus-one config"},
	},
	{
		ID:       "llm-overflow",
		Keywords: []string{"context", "too long", "tokens", "max_tokens", "context length", "overflow"},
		Title:    "Context overflow — history too long",
		Cause:    "The conversation exceeded the model's window. Nimbus One auto-compacts the oldest 30% and resends (up to 2 heals per step).",
		Fix:      "No action needed normally. If it recurs, start a fresh session — huge tool outputs (logs, dumps) are the usual cause; ask for summaries instead of full dumps.",
		Commands: []string{"nimbus-one run \"summarize our progress so far\""},
	},
	{
		ID:       "llm-nokeys",
		Keywords: []string{"no keys", "no usable keys", "exhausted", "no backend", "no provider"},
		Title:    "No usable LLM backend",
		Cause:    "No provider keys configured and local Ollama unreachable. Nimbus One refuses to guess — it tells you instead.",
		Fix:      "Recommended: OpenRouter + Meta Muse 1.3 (free tier). Or run Ollama locally for a fully offline setup. Your choice — Nimbus One applies nothing by itself.",
		Commands: []string{"nimbus-one config", "nimbus-one auto", "ollama pull llama3.1"},
	},
	{
		ID:       "ollama-down",
		Keywords: []string{"ollama", "localhost:11434", "connection refused ollama"},
		Title:    "Ollama not reachable",
		Cause:    "Nothing listens on localhost:11434 — Ollama isn't installed or isn't running.",
		Fix:      "Start Ollama (ollama serve) or install it; pull at least one model. On Android use the Ollama app or prefer OpenRouter keys instead.",
		Commands: []string{"ollama serve", "ollama pull llama3.1", "ollama list"},
	},
	{
		ID:       "opencode-missing",
		Keywords: []string{"opencode", "sidecar", "not found opencode"},
		Title:    "OpenCode sidecar unavailable",
		Cause:    "The `opencode` binary isn't on PATH, so heavy repo refactors can't delegate.",
		Fix:      "Install OpenCode, or keep working — Nimbus One's own tools cover everything except giant multi-file refactors. Correct headless form is `opencode run --format json \"task\"` (verified against 1.18.x; -p means --password there, not prompt).",
		Commands: []string{"opencode --version", "opencode run --format json \"hello\""},
	},
	{
		ID:       "tg-401",
		Keywords: []string{"telegram 401", "telegram unauthorized", "bot token invalid", "telegram 404"},
		Title:    "Telegram bot token rejected",
		Cause:    "Token wrong or bot deleted. Telegram answers 401/404 on getUpdates.",
		Fix:      "Talk to @BotFather, /mybots → API Token, store the fresh token. Then restrict the allowlist to your user id.",
		Commands: []string{"nimbus-one secrets set telegram <token>", "TELEGRAM_ALLOW_FROM=123456789 nimbus-one serve"},
	},
	{
		ID:       "tg-open",
		Keywords: []string{"allowlist", "allow from", "strangers", "anyone can talk", "open bot"},
		Title:    "Telegram bot open to anyone",
		Cause:    "Token set but no allowlist — any Telegram user can trigger your agent (and spend your keys).",
		Fix:      "Set TELEGRAM_ALLOW_FROM to your numeric id (message @userinfobot to learn it), restart serve.",
		Commands: []string{"TELEGRAM_ALLOW_FROM=123456789 nimbus-one serve"},
	},
	{
		ID:       "http-port",
		Keywords: []string{"port in use", "address already in use", "bind", "8787"},
		Title:    "HTTP port already in use",
		Cause:    "Another Nimbus One (or app) holds the port.",
		Fix:      "Stop the other instance, or pick another port in config.yaml (http_port) and restart.",
		Commands: []string{"nimbus-one doctor"},
	},
	{
		ID:       "http-lan-token",
		Keywords: []string{"lan token", "refusing to serve", "http_token", "bearer"},
		Title:    "LAN serve refused without token",
		Cause:    "Binding a non-loopback address without an HTTP token would expose your agent to the whole network. Nimbus One refuses instead of risking it.",
		Fix:      "Store a token (auto mode generates one) or bind 127.0.0.1 for local-only use.",
		Commands: []string{"nimbus-one secrets set http_token <random>", "nimbus-one auto"},
	},
	{
		ID:       "vault-corrupt",
		Keywords: []string{"vault", "decrypt", "corrupt", "wrong key", "secrets unreadable"},
		Title:    "Vault/secrets unreadable",
		Cause:    "vault.key doesn't match secrets.enc (restored backup? edited by hand?).",
		Fix:      "If you have the old vault.key, restore it. Otherwise delete vault.key + secrets.enc and re-run setup — you'll re-enter keys (nothing else is lost: workspace markdown is plaintext).",
		Commands: []string{"nimbus-one init --auto", "nimbus-one config"},
	},
	{
		ID:       "mdns-none",
		Keywords: []string{"mdns", "no peers", "discover", "lan not found"},
		Title:    "LAN discovery finds no peers",
		Cause:    "Hotspots and guest Wi-Fis often block multicast; some phones need Wi-Fi (not mobile data).",
		Fix:      "Nothing is broken — serve works via direct IP. Share http://<your-lan-ip>:8787 and the bearer token instead.",
		Commands: []string{"nimbus-one discover", "nimbus-one status"},
	},
	{
		ID:       "termux-wake",
		Keywords: []string{"wake", "sleep", "background", "killed", "termux suspends"},
		Title:    "Android suspends Nimbus One in background",
		Cause:    "Android freezes background apps. Nimbus One takes termux-wake-lock on serve, but the OS can still kill it.",
		Fix:      "Acquire Termux:API wake lock, disable battery optimization for Termux, and prefer a foreground session (or tmux) for long serves.",
		Commands: []string{"termux-wake-lock", "nimbus-one serve"},
	},
	{
		ID:       "skill-parse",
		Keywords: []string{"skill", "frontmatter", "SKILL.md", "skill not loading"},
		Title:    "Skill not loading",
		Cause:    "SKILL.md missing, bad frontmatter fences, or placed outside skills/ (needs skills/<name>/SKILL.md or skills/<cat>/<name>/SKILL.md).",
		Fix:      "Compare with the hello starter skill. Minimal valid file needs only a name + description; Hermes-strict fields (version/author) are optional for loading.",
		Commands: []string{"nimbus-one skills", "nimbus-one doctor"},
	},
	{
		ID:       "mcp-fail",
		Keywords: []string{"mcp", "stdio", "sse", "tool server", "external tools"},
		Title:    "MCP server won't connect",
		Cause:    "Binary missing, wrong args, or the server speaks SSE while Nimbus One dialed stdio (or vice versa).",
		Fix:      "Run the server command by hand first and watch its first lines — MCP servers must stay alive on stdio. Check version skew between client and server.",
		Commands: []string{"nimbus-one doctor"},
	},
	{
		ID:       "max-steps",
		Keywords: []string{"max steps", "exceeded", "looping", "keeps retrying", "stuck"},
		Title:    "Run hits max steps / loops",
		Cause:    "The task is too big for one loop, or a tool keeps failing the same way (guardrails warn at 2-3 identical failures).",
		Fix:      "Split the task and run the pieces. For coding refactors, delegate: ask for a plan first (--mode plan), then execute in build mode.",
		Commands: []string{"nimbus-one run --mode plan \"...\""},
	},
	{
		ID:       "net-timeout",
		Keywords: []string{"timeout", "network", "unreachable", "dns", "eof"},
		Title:    "Network timeouts talking to providers",
		Cause:    "Flaky mobile data, VPN, or captive portals. Nimbus One retries transient drops 3x with backoff+jitter automatically.",
		Fix:      "Retry once; if persistent, switch networks or fall back to local Ollama until it clears.",
		Commands: []string{"nimbus-one run \"hello\"", "nimbus-one doctor"},
	},
}

// Find returns up to n best-matching entries for a symptom query.
// Empty query returns nil. Matching is token-overlap on keywords+title.
func Find(query string, n int) []Entry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	if n <= 0 {
		n = 3
	}
	qtokens := tokenize(q)
	type scored struct {
		e Entry
		s int
	}
	var ss []scored
	for _, e := range Knowledge {
		hay := strings.ToLower(e.Title + " " + strings.Join(e.Keywords, " ") + " " + e.ID)
		score := 0
		for _, t := range qtokens {
			if t == "" {
				continue
			}
			if strings.Contains(hay, t) {
				score += len(t) // longer token matches weigh more
			}
		}
		// ID exact match jumps the queue.
		if strings.EqualFold(q, e.ID) {
			score += 1000
		}
		if score > 0 {
			ss = append(ss, scored{e, score})
		}
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].s > ss[j].s })
	if len(ss) > n {
		ss = ss[:n]
	}
	out := make([]Entry, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.e)
	}
	return out
}

func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '/' || r == '-' || r == '_')
	})
}
