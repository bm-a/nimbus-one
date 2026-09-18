# Nimbus-One

> **Beta (0.1.0-beta).** Everything works; flags and APIs may still shift.
> Pin setups to a checksum and report breakage with
> `nimbus-one doctor --bundle support.zip`.

A lightweight autonomous AI agent runtime in a single static binary.
The practical replacement for heavy OpenClaw / Hermes-class stacks —
no Node.js, no Python, no Chromium, no new hardware. Any Android phone
running Termux works. So does every laptop and server.

Pure Go. `CGO_ENABLED=0` everywhere. Your keys, your models, your choice —
Nimbus-One never picks fallbacks silently and never phones home.

## Quick Start

There is deliberately no one-line pipe-to-shell installer. Three explicit
steps, every one inspectable:

### Option A — Termux (Android)

```sh
pkg install golang git
git clone https://github.com/<you>/nimbus-one.git
cd nimbus-one
go build -o nimbus-one ./cmd/nimbus-one
./nimbus-one init --auto
./nimbus-one config
```

### Option B — Linux / macOS / Windows

1. Install Go 1.22+ from https://go.dev/dl/ (or your package manager).
2. Clone and build:
   ```sh
   git clone https://github.com/<you>/nimbus-one.git
   cd nimbus-one
   CGO_ENABLED=0 go build -o nimbus-one ./cmd/nimbus-one
   ```
3. Or grab a prebuilt binary from the GitHub Releases page and verify
   the checksum published alongside it before running.

### First run (all platforms)

```sh
./nimbus-one init --auto     # detect Ollama, OpenCode, network; seed workspace
./nimbus-one config          # visual wizard: provider, key, YOUR fallback order
./nimbus-one run "hello"     # first task
./nimbus-one serve           # HTTP API + Telegram + heartbeat daemon
```

## Commands

| Command | What it does |
|---|---|
| `init [--auto]` | Scaffold workspace, vault, starter skill. `--auto` detects everything and only fills blanks. |
| `auto` | Full automatic configuration with WARNINGS and a list of every change. |
| `config` | Visual setup wizard (guided prompts when no terminal). |
| `run [--mode plan\|build] [--model M] <msg>` | One-shot task. `plan` = read-only, `build` = full tools (default). |
| `exec` | Same as `run`, explicitly non-interactive. |
| `serve [--mode ...] [--no-mdns]` | HTTP API `:8787`, Telegram/Discord, heartbeat, LAN advert. |
| `skills` | List loaded skills and triggers. |
| `secrets set\|get\|del\|list` | Encrypted secret manager (AES-256-GCM, 0600 files). Values never listed. |
| `models` | OpenRouter catalog (offline suggestions when unreachable). |
| `discover` | Find Nimbus-One peers on your LAN via mDNS. |
| `doctor [--bundle FILE]` | 13 health checks; `--bundle` writes a REDACTED support zip. |
| `fix <symptom>` | Search the built-in fix-it database. Try `fix 429`. |
| `selftest` | Simulate 15 real-life failures, prove every recovery works. |
| `update [--yes]` | Pull GitHub releases. Asks first, backs up data, then replaces. |
| `speak <text>` / `transcribe <file>` | On-device TTS + voice-note STT (Telegram + web included). |
| `status` / `dashboard` | Text summary / live key-health TUI. |
| `version`, `help` | The obvious. |

Full detail per command: `docs/COMMANDS.md`. How it fits together:
`docs/ARCHITECTURE.md`. When something breaks: `nimbus-one fix …`,
`nimbus-one doctor`, then `docs/TROUBLESHOOTING.md`.

## Your models, your fallbacks

Nimbus-One ships with **zero pre-selected fallbacks**. The
`suggestions` list (starting with the recommended
`meta/muse-spark-1.3-contributor` route via OpenRouter free tier) is shown
in the wizard and docs — it is applied nowhere until you confirm it, write
`fallback_models` in `config.yaml`, or set `NIMBUS_FALLBACK_MODELS`.
Runtime failover only ever moves across keys and tiers you configured,
using deterministic latency + error-state routing. No token is ever spent
deciding where to route.

## Privacy

- Local-first: Markdown state, JSONL history, skills — all on your disk.
- Secrets encrypted at rest (vault key 0600, dirs 0700), redacted from
  every log and prompt, scrubbed from subprocess environments.
- LAN serve refuses to bind without a bearer token. Telegram needs an
  explicit allowlist. Support bundles redact by default.
- No telemetry. No accounts. No callbacks.

## Layout

```
cmd/nimbus-one/      CLI (init auto config run exec serve skills secrets
                     models discover doctor fix status dashboard)
internal/llm/        provider clients, pool/ balancer, resilience, OpenRouter
internal/engine/     ReAct loop, plan/build modes, context compaction
internal/state/      SOUL/USER/MEMORY/HEARTBEAT, JSONL store, hybrid recall
internal/skills/     SKILL.md compat, script exec, hot-reload watcher
pkg/mcp/             Model Context Protocol stdio client
internal/tools/      bash fs web builtins
internal/gateway/    broker REPL Telegram Discord HTTP
internal/daemon/     heartbeat scheduler Termux power hooks
internal/secure/     vault secrets redaction hardening
internal/setup/      auto-detect + embedded workspace templates
internal/netdiscover/ mDNS _nimbus._tcp advertise + discover
internal/doctor/     diagnostics bundles fix-it knowledge base
internal/selftest/   15-scenario fault simulation (also the selftest command)
internal/update/     consent-first self-update with data backup
internal/tui/        wizard dashboard escalation modal
default_workspace/   starter SOUL USER MEMORY HEARTBEAT + hello skill
docs/                operator manual for humans and AI agents
wiki/                exhaustive user + agent wiki (start at wiki/Home.md)
```

## Attribution & license

Ideas referenced (never copied) from OpenClaw, Hermes, OpenCode,
Charmbracelet, and fsnotify — see `ATTRIBUTION.md`. Working in this repo
with an AI agent? Read `AGENTS.md` first. MIT licensed (`LICENSE`).
