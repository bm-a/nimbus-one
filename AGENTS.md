# AGENTS.md — instructions for AI coding agents working in this repo

Read this before changing anything. It is the contract between you and the
maintainer. Human operator manual lives in `docs/`; command reference in
`docs/COMMANDS.md`; error fixes in `docs/TROUBLESHOOTING.md` and
`internal/doctor/knowledge.go` (queryable via `nimbus-one fix <symptom>`).

## What this is

Nimbus-One is a single-binary autonomous agent runtime in pure Go
(`CGO_ENABLED=0` always). Positioned as the lightweight replacement for
OpenClaw/Hermes-class stacks: runs on any Android phone, laptop, or server
with no Node.js, Python, Chromium, or new hardware.

## Non-negotiable rules

1. **Stdlib first.** New code uses only the standard library unless a pure-Go
   module is already in `go.mod` (bubbletea/lipgloss/bubbles/fsnotify,
   modernc.org/sqlite for the session store). Never add Cgo, never add
   network daemons, never add runtimes.
2. **No stubs.** No `TODO`, no placeholders, no dead code. If a platform
   can't do something, degrade with a warning, not a silent no-op.
3. **User chooses.** No hardcoded model fallback chains, no silent provider
   switching beyond pool failover across the user's own keys. Suggestions
   (see `llm.SuggestedModels`) are labeled and inert until confirmed in
   `nimbus-one config` or `fallback_models`.
4. **Verify everything.** After each change: `go vet` the touched packages,
   `go test -count=1` them, then `go build ./...`. Fix real failures —
   never weaken a test to make it pass.
5. **Secrets never leak.** Tests use fake keys. Logs/prompts go through
   `secure.Redact`. Files with secrets are 0600, dirs 0700.
6. **Cross-platform.** Every OS-specific call needs a `runtime.GOOS` branch
   or a documented graceful degradation. Test on android/arm64 here;
   cross-compile (`make cross`) before claiming portability.
7. **No pipe-to-shell installers.** Docs show explicit manual steps only.

## Layout

- `cmd/nimbus-one/main.go` — CLI: init/auto/config/run/exec/serve/skills/
  secrets/models/discover/doctor/fix/status/dashboard/version.
- `internal/llm/` — provider clients; `pool/` deterministic multi-key
  balancer; `resilience.go` backoff/escalation; `openrouter.go` catalog.
- `internal/engine/` — ReAct loop, plan/build modes, context compaction.
- `internal/state/` — SOUL/USER/MEMORY/HEARTBEAT files, JSONL store,
  BM25 + hashed-vector hybrid recall.
- `internal/skills/` + `pkg/mcp/` — SKILL.md compat (OpenClaw-minimal and
  Hermes-strict), script execution, MCP stdio client, hot-reload watcher.
- `internal/tools/` — bash/fs/web builtins behind the Tool interface.
- `internal/gateway/` — broker, REPL, Telegram, Discord, HTTP API.
- `internal/daemon/` — heartbeat scheduler, Termux power hooks.
- `internal/secure/` — vault (AES-256-GCM), secrets store, redaction.
- `internal/setup/` — auto-detect + embedded workspace templates.
- `internal/netdiscover/` — mDNS advertise/discover (`_nimbus._tcp`).
- `internal/doctor/` — diagnostics, redacted support bundles, knowledge base.
- `internal/tui/` — wizard, dashboard, escalation modal (bubbletea).
- `agents/` — reserved for future OpenCode subagent definitions (currently
  empty; do not invent agent files without being asked).
- `default_workspace/` — canonical starter files (binary embeds copies;
  `internal/setup/templates_test.go` fails on drift).

## Definition of done

`go vet ./...` clean, `go test -count=1 ./...` all ok,
`CGO_ENABLED=0 go build ./...` clean, plus a real-binary smoke test of
any touched command with an isolated `NIMBUS_DATA_DIR`.
