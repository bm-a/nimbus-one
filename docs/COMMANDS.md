# COMMANDS — complete operator reference

Every command also explains itself: `nimbus-one help [command]`.
Conventions: flags use `--name value` or `--name=value`. Exit 0 = ok,
1 = failed, 2 = usage error.

## init [--auto]

Creates the data dir (`~/.config/nimbus-one`, Termux `~/.nimbus-one`,
Windows `%APPDATA%/nimbus-one`), `vault.key` (0600), workspace
(`SOUL.md USER.md MEMORY.md HEARTBEAT.md`), and the starter `hello` skill.
Never overwrites existing files. `--auto` additionally probes Ollama,
OpenCode, provider keys, and LAN IPs, writes only missing values, and
prints WARNINGS + every action taken.

## auto

Same detection as `init --auto` but also generates an HTTP token when
serving the LAN would otherwise be refused, and hardens permissions.
Safe to re-run any time; it never overwrites your values.

## config

Visual Charmbracelet wizard: provider list → masked key entry with live
validation against `/v1/models` → fallback ordering (j/k move, Shift+J/K
reorder, x removes, enter confirms — empty means primary only) → save.
Keys go to the encrypted secrets store, model order to `fallback_models`
in `config.yaml`. Without a terminal it degrades to guided text prompts.
Nothing is ever applied that you didn't confirm.

## run / exec

```
nimbus-one run [--mode plan|build] [--model MODEL] <message...>
```

ReAct loop (≤25 steps, 3 retries per tool with error feedback), memory
recall injected into the prompt, every turn persisted to `sessions.db`
(SQLite) with tool-call pairing, MEMORY.md appended. `plan` hides
mutating tools and blocks them if attempted; `build` is full access.
Permission verdicts: deny blocks with guidance, ask denies headless
(fail-closed) — pre-approve via config `routes`/rules in later versions.
`exec` is identical, for scripts that want the explicit name. On total key
exhaustion: interactive escalation card on a terminal (new key / switch
provider / retry), diagnostics + recommendation otherwise. HTTP 402
(out of credits) prints an actionable fix instead of provider JSON.

Disclosed defaults in provider assembly (nothing silent, nothing paid):
your primary model, then your `fallback_models` in your order, then local
Ollama (free, on-device, no key) — skipped automatically if you already
listed it. With no keys at all, Ollama is the only entry.

## Agent tools (model-facing)

`delegate` (args: brief, agent=general|explore, model override,
background, context_mode): spawns a subagent — explore is read-only by
construction. `tasks_poll` manages background work. `apply_patch` takes
a multi-file envelope (GPT-family models see it instead of edit/write).
`git` (status|diff|log, read-only, workspace-scoped). `cron`
(schedule|list|cancel|due; in-memory only — restarts clear jobs).
`ask` interrupts for user input with timeout. Per-kind model routing via
`routes:` in config.yaml (e.g. `routes: explore=fast-model`) — unset
kinds inherit the primary; `status` shows active routes.

## serve [--mode ...] [--no-mdns]

Starts everything: HTTP API (`POST /api/v1/chat`, `POST /api/v1/task`,
`GET /api/v1/task?id=`, `GET /healthz`, bearer token when configured),
Telegram long-polling (needs token + `TELEGRAM_ALLOW_FROM`), Discord REST,
heartbeat ticker (battery-throttled on Termux), skill hot-reload,
Termux wake-lock, and mDNS `_nimbus._tcp` advertisement (skip with
`--no-mdns`). Refuses LAN binds without a token. Ctrl-C shuts down cleanly.

## skills

Prints the capability prompt: loaded skills with descriptions and trigger
patterns — the same text injected into the agent's system prompt.

## secrets set|get|del|list

AES-256-GCM encrypted store (`secrets.enc`, 0600). `list` shows key names
only, never values. Known keys: openrouter openai anthropic groq gemini
deepseek telegram discord http_token. `set` without a value prompts
(echo suppressed where the terminal allows).

## models

OpenRouter catalog with the suggested entry marked. Offline (or API
failure) falls back to the built-in suggestion list — clearly labeled,
never auto-applied.

## discover

5-second mDNS scan for `_nimbus._tcp` peers. Empty results are normal on
hotspots/guest Wi-Fi (multicast blocked) — serve still works via direct IP.

## doctor [--bundle FILE]

13 checks: toolchain, data dir writable, vault key present + 0600,
config parseable, secrets decryptable, Ollama, OpenCode, Telegram,
HTTP port free, disk probe, Termux APIs, MEMORY.md, skills non-empty.
Each failure prints a concrete fix. `--bundle` writes a redacted zip safe
to share alongside a support request.

## fix <symptom...>

Queries the built-in knowledge base (18 topics: 401/429/5xx, overflow,
no-backend, Ollama, OpenCode, Telegram, ports, tokens, vault, mDNS,
wake-lock, skills, MCP, loops, timeouts). No match → prints where to file
an issue with a `doctor --bundle` attached. Bare `fix` lists all topic ids.

## speak <text...> / transcribe <file>

`speak` plays text on local speakers (termux-tts-speak / say / espeak).
`transcribe` prints the transcript of an audio file — Telegram `.ogg`
voice notes work as-is, no ffmpeg: whisper.cpp → `NIMBUS_STT_URL` →
OpenAI Whisper, else exact setup steps. Same engines back Telegram voice
notes (auto-transcribed into chat), `/speak <text>` voice replies,
`POST /api/v1/transcribe` (multipart `audio`), `POST /api/v1/speak`
(`{"text"}` → audio bytes), and the web console mic/speaker buttons
(browser capture + speech synthesis, server engines as fallback).

## status / dashboard

`status`: version, paths, model, mode default, HTTP bind, heartbeat
interval, skill/tool counts, providers WITH keys (names only), channel
states, plus warnings (no keys, open Telegram bot). `dashboard`: live
key-health TUI (pool states, latencies, cooldowns), needs a terminal.

## Environment

`NIMBUS_DATA_DIR NIMBUS_MODEL NIMBUS_FALLBACK_MODELS NIMBUS_LOG_LEVEL
NIMBUS_HTTP_TOKEN NIMBUS_VAULT_KEY TELEGRAM_BOT_TOKEN TELEGRAM_ALLOW_FROM
DISCORD_BOT_TOKEN OPENROUTER_API_KEY OPENAI_API_KEY ANTHROPIC_API_KEY
GROQ_API_KEY GEMINI_API_KEY DEEPSEEK_API_KEY META_API_KEY OLLAMA_BASE_URL
OPENAI_BASE_URL OPENCODE_BIN`
