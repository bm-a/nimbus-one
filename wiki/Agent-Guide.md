# Agent Guide

For AI coding agents working **with** nimbus-one
(as a tool / sidecar) or **in** this repo (as a contributor).

Human docs: [User Guide](User-Guide.md) ·
[Operator Guide](Operator-Guide.md) ·
[Mobile Termux](Mobile-Termux.md) · [FAQ](FAQ.md).

Repo contract: `AGENTS.md` (read it before changing code).
Command reference: `docs/COMMANDS.md`.
Error fixes: `docs/TROUBLESHOOTING.md` +
`internal/doctor/knowledge.go` (queryable via `nimbus-one fix`).

> **Suggestion vs user choice:** `llm.SuggestedModels`
> starts with `meta/muse-spark-1.3-contributor` (Muse route =
> *recommendation*). Fallbacks are *user-chosen*:
> `config.yaml fallback_models`, `NIMBUS_FALLBACK_MODELS`,
> or wizard-confirmed order. Never invent or apply a chain.

## Command cheat-sheet

Verified against `cmd/nimbus-one/main.go`
(`helpText` map + `cmd*` functions). Flags use
`--name value` or `--name=value`. Exit 0 = ok,
1 = failed, 2 = usage error.
**These are the only flags — do not invent others.**

| Command | Real flags | Notes |
|---|---|---|
| `init [--auto]` | `--auto` | Scaffold only; `--auto` also probes + fills blanks. Never overwrites. |
| `auto` | none | Full auto-config; generates LAN HTTP token if needed. Re-runnable. |
| `config` | none | Visual wizard; guided text prompts without TTY. |
| `run [--mode plan\|build] [--model M] <msg>` | `--mode`, `--model` | Default mode `build`. ReAct ≤25 steps. |
| `exec ...` | `--mode`, `--model` | Identical to `run`. |
| `serve [--mode ...] [--no-mdns]` | `--mode`, `--no-mdns` | HTTP + Telegram/Discord + heartbeat + wake-lock + mDNS. |
| `skills` | none (extra args ignored) | Prints capability prompt. |
| `secrets set\|get\|del\|list` | none (`delete` = alias of `del`) | `set <key> [value]` prompts when value omitted. |
| `models` | none | OpenRouter catalog; offline fallback list. |
| `discover` | none | 5s mDNS scan for `_nimbus._tcp`. |
| `doctor [--bundle FILE]` | `--bundle` | 13 checks; `--bundle` writes redacted zip. |
| `update [--yes] [--repo OWNER/NAME]` | `--yes`, `--repo` | Consent-first; `--repo` or `NIMBUS_REPO` overrides placeholder. |
| `fix <symptom...>` | none | Bare `fix` lists all topic IDs. |
| `status` / `dashboard` | none | `dashboard` needs a TTY. |
| `version`, `--version`, `-v` | — | Prints `nimbus-one 0.1.0 (os/arch)`. |
| `help`, `--help`, `-h` | `[command]` | `help <command>` for `init auto config run serve doctor` detail. |

HTTP surface (for agents driving a `serve` instance):

- `POST /api/v1/chat` `{"message","user?","mode?"}` → `{"reply"}`.
- `POST /api/v1/task` `{"task","user?"}` → `{"id"}`; `GET /api/v1/task?id=` → `{"id","done","reply"}`.
- `GET /healthz` → `ok`. `GET /` → console. `GET /api/v1/stream?message=&user=` → SSE (`delta` + `done`).
- Auth: `Authorization: Bearer <token>` (stream also `?token=`).

REPL: `/help /mode [plan|build] /reset /exit`.

## Error policy (opencode-delegate vs pull-files)

The `opencode` tool contract (`internal/tools/opencode.go`):
**either OpenCode handles it directly, or the agent pulls
the files and fixes them with its own tools. There is no third path.**

- Large multi-file coding/refactor → `opencode` tool
  (`task` required, `dir` optional; correct headless form
  `opencode run --format json "task"` — note: `-p` means
  `--password` in OpenCode 1.18.x, not prompt).
- Small targeted fixes → `read` / `write` / `bash` directly.
- `opencode` binary missing (`OPENCODE_BIN` unset, not on
  `PATH`) → message says: pull files instead (`list` /
  `search` → `read` → `write`/`bash`), install from
  `https://opencode.ai` to enable delegation.
- Delegation failure → error tells you to fall back to
  pulling files and fixing directly.
- Empty OpenCode output → verify the working tree yourself
  before reporting.
- Same pattern for subagents: `delegate` (bounded brief,
  `background=true` for long work) then `tasks_poll`
  (`poll|list|cancel`). Depth cap 2 — subagents cannot
  spawn subagents; the refusal message tells them to
  finish the piece themselves.

## Plan/build discipline

- Default is `build`. Unknown `--mode` normalizes to `build`.
- Plan mode hides mutating tools from the model AND blocks
  them if attempted — describe changes instead of making them.
- Read-only set: `read list search web_fetch web_search`.
- Mutating (build only): `bash write` + skill scripts +
  MCP writes + `opencode` + `delegate`.
- Working in this repo: inspect in plan first for risky
  changes; high-stakes mutations get a proposed plan
  before execution (per `SOUL.md` boundaries).
- After code changes, definition of done (`AGENTS.md`):
  `go vet` touched packages → `go test -count=1` them →
  `go build ./...` → real-binary smoke test of any touched
  command with isolated `NIMBUS_DATA_DIR`.
- Stdlib first; pure-Go deps only if already in `go.mod`;
  no Cgo, no stubs/`TODO`, `secure.Redact` on logs/prompts,
  0600 secrets / 0700 dirs, `runtime.GOOS` branches for OS code.

## Skills format

Minimal valid skill (OpenClaw-minimal shape):

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

Strict shape (Hermes-strict) also accepted:

```md
---
name: hello
description: Greets the user and proves skill loading works.
version: 1.0.0
author: Nimbus One
triggers: hello, hi, greet
params:
  name:
    type: string
    description: Who to greet.
    required: false
---

# Hello skill
...
```

Rules (from `internal/skills/parser.go`):

- Path: `skills/<name>/SKILL.md` or
  `skills/<cat>/<name>/SKILL.md` (`SKILL.md` or `skill.md`).
- Frontmatter `---` fences optional — falls back to
  directory name + first heading / first non-empty line.
- Only `name` + `description` required for loading;
  `version`/`author`/`triggers`/`params` optional;
  unknown keys (e.g. `platforms`) ignored, never fatal.
- Names lowercased + slugified (spaces/underscores → `-`).
- Body = prompt text; optional shell snippets run with
  `SKILL_ARG_<name>` env + timeouts.
- Loaded from `<data-dir>/skills` + `<workspace>/skills`;
  hot-reloaded on `serve` (fsnotify with polling fallback).
- Debug: `nimbus-one skills` (capability prompt),
  `nimbus-one doctor` (skills check), `nimbus-one fix skill`.

## Knowledge base (all 18 IDs)

Source: `internal/doctor/knowledge.go` `Knowledge`
(mirrored in `docs/TROUBLESHOOTING.md`).
Query: `nimbus-one fix <symptom>` (top-3 token-overlap;
exact ID jumps the queue). Bare `fix` lists IDs.

| ID | Title | One-line fix |
|---|---|---|
| `llm-401` | Provider rejects API key (401/403) | `nimbus-one config`, or `secrets set openrouter <key>` (key marked Dead, replace it). |
| `llm-429` | Rate limited (429), keys cooling | Wait or add a second key; watch `nimbus-one dashboard`. |
| `llm-5xx` | Provider outage (5xx) | Breaker fails over after 3x; check status page, reorder fallbacks if prolonged. |
| `llm-overflow` | Context overflow | Auto-compacts oldest 30% (≤2 heals); if recurrent, summarize instead of pasting dumps. |
| `llm-nokeys` | No usable LLM backend | Your choice: `config` (suggested OpenRouter + Muse) or `ollama pull llama3.1`. |
| `ollama-down` | Ollama not reachable | `ollama serve` + `ollama pull llama3.1`; on Android prefer provider keys. |
| `opencode-missing` | OpenCode sidecar unavailable | Optional; correct form `opencode run --format json "task"`. |
| `tg-401` | Telegram bot token rejected | Fresh token from @BotFather → `secrets set telegram <token>`. |
| `tg-open` | Telegram bot open to anyone | Set `TELEGRAM_ALLOW_FROM=<your-id>`, restart serve. |
| `http-port` | HTTP port already in use | Stop the other instance or change `http_port` in `config.yaml`. |
| `http-lan-token` | LAN serve refused without token | `secrets set http_token <random>` (or `auto`), or bind `127.0.0.1`. |
| `vault-corrupt` | Vault/secrets unreadable | Restore matching `vault.key`, else delete key + `secrets.enc` and redo setup. |
| `mdns-none` | LAN discovery finds no peers | Multicast blocked — use direct IP, nothing is broken. |
| `termux-wake` | Android suspends app | `termux-wake-lock`, disable battery optimization, prefer foreground/tmux. |
| `skill-parse` | Skill not loading | Needs `skills/<name>/SKILL.md` with name + description; compare with `hello`. |
| `mcp-fail` | MCP server won't connect | Run server command by hand; confirm stdio vs SSE; check versions. |
| `max-steps` | Run hits max steps / loops | Split task; `run --mode plan` first, then build. |
| `net-timeout` | Network timeouts | Auto-retried 3x; still failing → switch networks or local Ollama. |

Doctor suite (13 checks, `internal/doctor/doctor.go`):
`go_version data_dir vault_key config secrets ollama
opencode telegram http_port disk termux memory skills`.

## Escalation behavior

When every key across every tier fails (`pool.ExhaustedError`):

- TTY: interactive card — new key (prompt + save +
  retry) / switch provider (`config`, then retry) / retry now.
- Non-TTY: diagnostics (`provider= keys= cooling= dead= last=…`)
  + recommendation (default: Muse route via OpenRouter
  free tier or Meta endpoint — suggestion, not auto-action).
- Keep `AGENTS.md` rule 3: failover only across the
  user's own keys; suggestions stay inert until confirmed.

## Secrets rules

- Tests use fake keys. Never paste real keys into code,
  tests, logs, prompts, issues, or bundles.
- All log/prompt paths go through `secure.Redact`.
- Subprocess env is scrubbed of secrets.
- Files: `vault.key` / `secrets.enc` 0600, data dir
  hardened (0700). `doctor --bundle` redacts by default.
- `secrets list` shows names only; `secrets get` prints
  the value only because the user explicitly asked,
  on their own terminal.

Related: [User Guide](User-Guide.md) ·
[Operator Guide](Operator-Guide.md) · [FAQ](FAQ.md).
