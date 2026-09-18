# Nimbus One Wiki — Home

Nimbus One is a lightweight autonomous AI agent in one
static Go binary (`CGO_ENABLED=0`).

It is the practical replacement for heavy
OpenClaw / Hermes-class stacks: no Node.js, no Python,
no Chromium, no new hardware.

Any Android phone running Termux works.
So does every laptop and server.

- Your keys, your models, your choice.
- Zero pre-selected fallbacks — nothing is applied silently.
- Local-first: Markdown state, JSONL history, skills on your disk.
- No telemetry. No accounts. No callbacks.

> **Suggestion vs your choice:** the Muse route
> (`meta/muse-spark-1.3-contributor` via OpenRouter)
> is a *recommendation* shown in the wizard, `models`,
> and escalation cards. Fallbacks are *always your choice* —
> confirmed in `nimbus-one config`, `fallback_models`,
> or `NIMBUS_FALLBACK_MODELS`. Never auto-applied.

## Wiki map

- [User Guide](User-Guide.md) — setup, plan/build, chat,
  serve, Telegram, skills, secrets, every knob.
- [Operator Guide](Operator-Guide.md) — hardening, backups,
  updates, monitoring, battery, LAN + mDNS.
- [Agent Guide](Agent-Guide.md) — for AI coding agents working
  with nimbus-one or in this repo. Cheat-sheet, error policy,
  plan/build discipline, skills format, all 18 knowledge IDs.
- [Mobile Termux](Mobile-Termux.md) — phone-first ops:
  install, footprint, wake-lock, throttling, offline Ollama.
- [Voice](Voice.md) — speech-to-text and text-to-speech: Telegram voice
  notes, `/speak` replies, web console mic/speaker, engines and setup.
- [FAQ](FAQ.md) — 15+ honest answers on fallbacks, privacy,
  cost, platforms, no-keys mode, data loss, updates.

Source truth: `README.md`, `AGENTS.md`, `docs/COMMANDS.md`,
`docs/ARCHITECTURE.md`, `docs/TROUBLESHOOTING.md`,
`internal/doctor/knowledge.go`.

## 60-second quick start

Build first (explicit steps only — no pipe-to-shell installer):

```sh
git clone https://github.com/<you>/nimbus-one.git
cd nimbus-one
go build -o nimbus-one ./cmd/nimbus-one
```

Then:

```sh
./nimbus-one init --auto
./nimbus-one config
./nimbus-one run "hello"
./nimbus-one serve
```

What each step does:

1. `init --auto` — scaffolds data dir + workspace,
   probes Ollama / OpenCode / keys / LAN, fills blanks only.
2. `config` — visual wizard: provider, masked key entry
   with live validation, YOUR fallback order.
3. `run "hello"` — first one-shot task (default mode: build).
4. `serve` — HTTP API on `127.0.0.1:8787` + Telegram
   (if configured) + heartbeat + LAN discovery.

Next: pick [User Guide](User-Guide.md) for daily use,
[Mobile Termux](Mobile-Termux.md) if you are on a phone,
or [Agent Guide](Agent-Guide.md) if you are an AI agent.

## Commands at a glance

All verified against `cmd/nimbus-one/main.go`.
Full detail: `nimbus-one help [command]` and
[Agent Guide](Agent-Guide.md#command-cheat-sheet).

- `init [--auto]` · `auto` · `config` — setup.
- `run [--mode plan|build] [--model M] <msg>` · `exec` — tasks.
- `serve [--mode ...] [--no-mdns]` — daemon + API + channels.
- `skills` · `secrets set|get|del|list` · `models` · `discover`
- `doctor [--bundle FILE]` · `fix <symptom>` · `update [--yes] [--repo OWNER/NAME]`
- `status` · `dashboard` · `version` · `help`

See also: [FAQ](FAQ.md) · [Operator Guide](Operator-Guide.md).
