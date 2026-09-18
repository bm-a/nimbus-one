# TROUBLESHOOTING — mirrors `nimbus-one fix` / `internal/doctor/knowledge.go`

Fastest path: `nimbus-one fix <symptom>` (e.g. `fix 429`), then
`nimbus-one doctor`. Keep this file in sync with `knowledge.go`.

## Provider rejects the API key (401/403) — `llm-401`

Key wrong, revoked, or pasted with whitespace. The key is marked Dead and
never retried silently. Fix with the live-validating wizard:
`nimbus-one config`, or `nimbus-one secrets set openrouter <key>`.

## Rate limited (429) — `llm-429`

Traffic already moved to your next key; the hot key rejoins after
Retry-After. Add a second key for the same provider, or spread models
across providers. Watch `nimbus-one dashboard`.

## Provider outage (5xx) — `llm-5xx`

Circuit breaker trips after 3 consecutive failures and fails over to your
next tier. Check the provider status page; reorder fallbacks if prolonged.

## Context overflow — `llm-overflow`

Auto-healed: oldest 30% compacted, resent (≤2 heals). If recurrent, stop
pasting full dumps — ask for summaries — or start a fresh session.

## No usable backend — `llm-nokeys`

No keys + Ollama unreachable. Your call: `nimbus-one config` (suggested:
OpenRouter + Meta Muse 1.3 free tier) or local Ollama (`ollama pull llama3.1`).

## Ollama not reachable — `ollama-down`

Start it (`ollama serve`) and pull a model. On Android without Ollama,
prefer provider keys instead.

## OpenCode sidecar unavailable — `opencode-missing`

Optional component. Install OpenCode for giant refactors; everything else
works without it. Correct headless form: `opencode run --format json "task"`.

## Telegram token rejected — `tg-401`

Fresh token from @BotFather → `nimbus-one secrets set telegram <token>`.

## Telegram open to anyone — `tg-open`

Set `TELEGRAM_ALLOW_FROM=<your-numeric-id>` and restart serve.

## HTTP port in use — `http-port`

Stop the other instance or change `http_port` in `config.yaml`.

## LAN serve refused without token — `http-lan-token`

By design. `nimbus-one secrets set http_token <random>` (auto mode
generates one) or bind `127.0.0.1`.

## Vault/secrets unreadable — `vault-corrupt`

Restore the matching `vault.key`, or delete key + `secrets.enc` and redo
setup (Markdown state is plaintext and survives).

## Discovery finds no peers — `mdns-none`

Multicast blocked (hotspots/guest Wi-Fi). Use direct IP — nothing is broken.

## Android suspends the app — `termux-wake`

`termux-wake-lock`, disable battery optimization for Termux, prefer
foreground/tmux sessions for long serves.

## Skill not loading — `skill-parse`

Needs `skills/<name>/SKILL.md` (or `skills/<cat>/<name>/SKILL.md`) with at
least name + description. Compare with the `hello` starter skill.

## MCP server won't connect — `mcp-fail`

Run the server command by hand first; confirm stdio vs SSE; check versions.

## Max steps / looping — `max-steps`

Split the task. Plan first (`run --mode plan`), execute in build mode.

## Network timeouts — `net-timeout`

Auto-retried 3x with backoff. Still failing: switch networks or use local
Ollama until it clears.
