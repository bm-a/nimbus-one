# Changelog

## Unreleased — nimbus-min providers (OpenClaw parity)

46 providers from OpenClaw's `modelCatalog` manifests as a data table
(two clients: native Anthropic + generic OpenAI chat-completions; the
loop is provider-blind). `models` command, onboard provider/model
choice, `--provider/--model/--base-url` overrides, `NIMBUS_PROVIDER/
_MODEL/_BASE_URL/_API_KEY` env, local Ollama/LM Studio/llama-server,
custom-server escape hatch. Network pin is per-provider (keys can't
cross hosts). Excluded with reasons: SDK-auth clouds, OAuth CLIs,
ambiguous endpoints.

## Unreleased — nimbus-min 0.1.0 (the product)

Minimal coding assistant: one workspace, five tools
(read/write/edit/list/shell), mandatory typed-`yes` shell gate, jail
with symlink-escape defense, per-provider network pin in code, OpenClaw app protocol subset (`connect`/`chat.send`/
`sessions.list`) + green control page, `onboard` wizard, tested
installers (`nimbus-min/install.sh`, `install.ps1`), full wiki.
Pure Go, zero dependencies, ~10MB static binary. See
`nimbus-min/README.md`, `SECURITY.md`, `DECISIONS.md`.

## 0.1.0-beta — first public snapshot (beta)

Beta: everything works, APIs/flags may still shift based on feedback.
Pin daily-use setups to a checksum; report breakage with
`nimbus-one doctor --bundle support.zip`.

- Single static binary (`CGO_ENABLED=0`): android-arm64, linux amd64/arm64,
  darwin-arm64, windows-amd64.
- ReAct engine with plan/build modes, context compaction + overflow healing.
- Deterministic multi-key pool: latency routing, 429/503 cooldowns with
  header parsing, 401 exile, background prober with switch-back.
- Skills (OpenClaw/Hermes `SKILL.md`), MCP stdio client, fsnotify hot-reload.
- Gateway: REPL, Telegram (allowlist, voice notes, `/speak`), Discord,
  HTTP API + mobile web console + SSE + STT/TTS endpoints.
- Voice: whisper.cpp / OpenAI STT, local + API TTS, `speak`/`transcribe`.
- Daemon: HEARTBEAT.md ticker, Termux wake-lock + battery throttling, mDNS.
- Privacy: AES-256-GCM vault + secrets, redaction, hardening, consent-first
  self-update with backup.
- Trust: `doctor` (13 checks), `fix` knowledge base (18 topics), 15-scenario
  `selftest`, 200+ unit tests.
- Docs: README, `docs/`, 7-page `wiki/`, `AGENTS.md`.
