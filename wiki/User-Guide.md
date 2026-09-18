# User Guide

For humans using Nimbus One daily.
Operators: see [Operator Guide](Operator-Guide.md).
Agents: see [Agent Guide](Agent-Guide.md).
Phones: see [Mobile Termux](Mobile-Termux.md).

> **Choice rule:** fallbacks are *your choice*.
> The Muse route (`meta/muse-spark-1.3-contributor`
> via OpenRouter) is a *suggestion* — shown in the
> wizard, `models` output, and escalation cards,
> applied nowhere until you confirm it.

## Setup paths compared

Three commands, one decision: how much do you want to choose?

| Path | Command | What it does |
|---|---|---|
| Scaffold | `nimbus-one init` | Creates data dir, `vault.key` (0600), workspace (`SOUL.md USER.md MEMORY.md HEARTBEAT.md`), starter `hello` skill. Never overwrites. |
| Scaffold + detect | `nimbus-one init --auto` | Above + probes Ollama, OpenCode, provider keys, LAN IPs. Writes only missing values. Prints WARNINGS + every action. |
| Automatic | `nimbus-one auto` | Same detection, also generates an HTTP token when LAN serve would otherwise be refused, hardens permissions. Safe to re-run any time; never overwrites your values. |
| Visual | `nimbus-one config` | Charmbracelet wizard: provider list → masked key entry with live validation → fallback ordering (empty = primary only) → save. No terminal? Falls back to guided text prompts. |

Typical first run:

```sh
nimbus-one init --auto
nimbus-one config
nimbus-one run "hello"
nimbus-one doctor
```

Where things live:

- Termux: `~/.nimbus-one`
- Linux/macOS: `~/.config/nimbus-one` (or `$XDG_CONFIG_HOME/nimbus-one`)
- Windows: `%APPDATA%/Nimbus One`
- Override any platform: `NIMBUS_DATA_DIR=/path/to/dir`

Inside the data dir: `config.yaml`, `vault.key`,
`secrets.enc`, `workspace/`, `skills/`.

## Plan vs build with examples

Default mode is **build** (full tools).
**Plan** is read-only: mutating tools are hidden and
blocked with a message instead of executing.

Read-only in plan mode: `read`, `list`, `search`,
`web_fetch`, `web_search`. Everything else
(`bash`, `write`, skill scripts, MCP writes,
`opencode`, `delegate`) needs build mode.

```sh
# Inspect first — changes nothing
nimbus-one run --mode plan "what would you change in main.go?"

# Then execute
nimbus-one run --mode build "refactor main.go as you proposed"

# Explicit non-interactive twin of run (scripts, cron)
nimbus-one exec --mode plan "summarize HEARTBEAT.md"

# One-off model override (does not save)
nimbus-one run --model openai/gpt-4o-mini "hello"
```

Live switching:

- REPL: `/mode plan` or `/mode build` (`/help`, `/reset`, `/exit` also exist).
- HTTP: `POST /api/v1/chat` with `{"mode":"plan"}` (switches process-wide mode).
- Serve default: `nimbus-one serve --mode build`.

Rule of thumb: `plan` first for anything destructive
or expensive, `build` to do the work.
See [Agent Guide](Agent-Guide.md#planbuild-discipline).

## Everyday flows

### Chat (one-shot)

```sh
nimbus-one run "summarize our progress so far"
nimbus-one run --mode plan "audit this repo for secrets"
```

ReAct loop: max 25 steps, 3 retries per tool with
error feedback. Memory recall is injected, turns are
saved, `MEMORY.md` gets a Q/A snippet appended.

No keys at all? You get guidance, not silence —
see [FAQ](FAQ.md#no-keys-mode).

### Serve (always-on agent)

```sh
nimbus-one serve
nimbus-one serve --mode plan
nimbus-one serve --no-mdns
```

Starts: HTTP API + Telegram polling (if a token is
configured) + Discord + heartbeat ticker +
skill hot-reload + Termux wake-lock (on Android) +
mDNS `_nimbus._tcp` advertisement (skip with `--no-mdns`).
`Ctrl-C` stops cleanly.

HTTP endpoints (all on `http_bind:http_port`, default `127.0.0.1:8787`):

- `POST /api/v1/chat` `{"message":"hi","user":"me"}` → `{"reply":"..."}`.
- `POST /api/v1/task` `{"task":"..."}` → `{"id":"1"}` (async).
- `GET /api/v1/task?id=1` → `{"id":"1","done":true,"reply":"..."}`.
- `GET /healthz` → `ok`.
- `GET /` → mobile-first web console (token field, localStorage, no CDNs).
- `GET /api/v1/stream?message=hi` → SSE (one `delta` + `done` event; honest non-token streaming).

Auth: `Authorization: Bearer <token>` (stream also
accepts `?token=` because EventSource cannot set headers).

```sh
curl -H "Authorization: Bearer $TOKEN" \
  localhost:8787/api/v1/chat -d '{"message":"hello"}'
```

### Telegram

```sh
nimbus-one secrets set telegram <token>
TELEGRAM_ALLOW_FROM=123456789 nimbus-one serve
```

Get the token from @BotFather.
Find your numeric id via @userinfobot.
Empty allowlist = anyone can talk to your bot
(and spend your keys) — `status` and `doctor` warn.
Hardening: [Operator Guide](Operator-Guide.md#serve-hardening).

### Skills

```sh
nimbus-one skills
```

Prints the capability prompt: loaded skills with
descriptions and triggers — the same text injected
into the agent's system prompt.
Hot-reloaded on `serve` (no restart; watch for
`skills reloaded` on stderr).

10-line skill (minimal valid file):

```md
---
name: hello
description: Greets the user and proves skill loading works.
triggers: hello, hi, greet
---

# Hello skill

When the user greets you, respond warmly and mention
one capability from the tool list.
```

Location: `skills/<name>/SKILL.md` or
`skills/<cat>/<name>/SKILL.md` (also `skill.md`
lowercase accepted). Strict fields (`version`,
`author`, `params`) are optional.
Unknown frontmatter keys (e.g. `platforms`) are ignored.
Full format: [Agent Guide](Agent-Guide.md#skills-format).

### Secrets

```sh
nimbus-one secrets set openrouter <key>
nimbus-one secrets set telegram
nimbus-one secrets get openrouter
nimbus-one secrets del openrouter
nimbus-one secrets list
```

- AES-256-GCM store at `secrets.enc` (0600).
- `list` shows key names only, never values.
- `set` without a value prompts (echo suppressed where possible).
- `del` also accepts `delete`.
- Known keys: `openrouter openai anthropic groq gemini deepseek telegram discord http_token`.
- Lookup order everywhere: secrets store → `config.yaml` → env var.
- Vault key: `NIMBUS_VAULT_KEY` env or `vault.key` file (0600).

## Customization-first: every knob

### config.yaml keys

Managed keys (the only ones `config` writes;
unknown keys and comments are preserved):

- `primary_model` — default `gpt-4o-mini`.
- `fallback_models` — comma-separated, YOUR order.
  Empty = primary only. Never auto-populated.
- `http_port` — default `8787`.
- `http_bind` — default `127.0.0.1`.
- `heartbeat_every` — Go duration, default `15m`.
- `telegram_token` — prefer secrets store instead.

`fallback_models` is *your choice*.
To adopt the suggestion, write it yourself:

```yaml
primary_model: meta/muse-spark-1.3-contributor
fallback_models: openrouter/auto, anthropic/claude-sonnet-4, openai/gpt-4o-mini
```

### Env vars

| Var | Effect |
|---|---|
| `NIMBUS_DATA_DIR` | Override data dir. |
| `NIMBUS_MODEL` | Override primary model. |
| `NIMBUS_FALLBACK_MODELS` | Comma-separated fallback order (your choice). |
| `NIMBUS_LOG_LEVEL` | Log verbosity. |
| `NIMBUS_HTTP_TOKEN` | Bearer token for HTTP API. |
| `NIMBUS_VAULT_KEY` | Vault key instead of file. |
| `NIMBUS_REPO` | Override update repo (`OWNER/NAME`). |
| `NIMBUS_SUPPORT_URL` | Override support endpoint. |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_ALLOW_FROM` | Telegram token / comma-separated allowlist. |
| `DISCORD_BOT_TOKEN` | Discord token. |
| `OPENROUTER_API_KEY` `OPENAI_API_KEY` `ANTHROPIC_API_KEY` `GROQ_API_KEY` `GEMINI_API_KEY` `DEEPSEEK_API_KEY` `META_API_KEY` | Provider keys. |
| `OLLAMA_BASE_URL` `OLLAMA_HOST` `OLLAMA_MODEL` | Local-model opt-in / endpoint / model. |
| `OPENAI_BASE_URL` | Custom OpenAI-compatible endpoint. |
| `OPENCODE_BIN` | Path to `opencode` binary. |

Provider assembly (nothing silent, nothing paid beyond
your keys): your primary → your `fallback_models` in
your order → local Ollama *only if you opted in*
(model name or `OLLAMA_*` env). Entries without a
usable key are skipped with a warning.

### fallback_models (your choice, in detail)

- Set via wizard (`nimbus-one config`), `config.yaml`,
  or `NIMBUS_FALLBACK_MODELS`.
- `nimbus-one models` shows the catalog with the
  suggested entry marked — clearly labeled, never applied.
- Pool routes by latency EMA + error state (healthy /
  degraded / cooling / dead). 401 kills a key, 5xx trips
  the breaker after 3 consecutive failures.
- Exhaustion path: interactive card on a TTY
  (new key / switch provider / retry), diagnostics +
  recommendation otherwise.

### Heartbeat

- Checklist file: `workspace/HEARTBEAT.md`, `- [ ]` lines run.
- Interval: `heartbeat_every` (`15m` default; any Go duration).
- Reply `NO_REPLY` / `HEARTBEAT_OK` = stay quiet.
- Battery-aware on Termux (see [Mobile Termux](Mobile-Termux.md)).

### Web console options

- Served at `GET /` whenever `serve` runs — same bytes
  whether or not a token is set (never leaks token state).
- Token field persisted in browser localStorage.
- `?title=` query overrides the page title.
- No JS? `POST` JSON to `/api/v1/chat` with curl instead.
- LAN/offline friendly: vanilla JS, no CDNs, under 14KB.

Related: [Operator Guide](Operator-Guide.md) ·
[FAQ](FAQ.md) · [Mobile Termux](Mobile-Termux.md).
