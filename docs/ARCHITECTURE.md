# ARCHITECTURE — how Nimbus-One fits together

Single static binary (`CGO_ENABLED=0`). Request flow for one message:

```
channel (REPL/Telegram/Discord/HTTP)
  → gateway.Broker (session isolated per channel/user)
    → engine (system prompt = SOUL+USER+MEMORY+recalled facts+skill caps)
      → ReAct loop → llm.Provider (default: pool.Pool)
        → pool picks healthiest user-configured key by latency EMA
        → provider client (OpenAI-compatible SSE) or Ollama or OpenCode sidecar
      → tools (builtins + skills + MCP) with 3-attempt error feedback
    → reply recorded, turns stored, MEMORY.md appended
```

## Key subsystems

- **llm/pool**: per-key state machine (Healthy/Degraded/CoolingDown/Dead),
  latency EMA routing, `Retry-After`/`x-ratelimit-reset` parsing, 401 kills,
  5xx circuit-breaking, deterministic tier failover across the USER's list.
- **llm/resilience**: backoff with full jitter (cap 5s), transient retries,
  `ContextOverflowError` (engine compacts oldest 30%, resends ≤2x),
  `ExhaustedError` → TUI escalation card.
- **engine**: OODA loop, plan/build modes (process-wide, atomic), sliding
  compactor at 80% context, model routing hints, self-reflection suffix.
- **state**: flat Markdown identity + JSONL turns/facts + BM25/hashed-vector
  hybrid recall fused by reciprocal rank. No Cgo database: the Store
  interface allows swapping backends later.
- **skills + pkg/mcp**: tolerant SKILL.md parser (OpenClaw-minimal through
  Hermes-strict), shell/script execution with timeouts, stdio MCP client,
  fsnotify hot-reload with polling fallback.
- **gateway**: one Broker, isolated sessions, chunked senders per channel.
- **daemon**: HEARTBEAT.md checklist ticker, Termux wake-lock, battery
  throttling (<20% → 3x interval, <10% → 6x).
- **secure**: vault key (env or 0600 file, auto-created), encrypted secrets,
  redaction everywhere, 0700/0600 hardening, subprocess env scrubbing.
- **tui**: bubbletea wizard, health dashboard, escalation modal — the only
  non-stdlib UI code, all pure Go.
- **doctor**: 13 checks, redacted bundles, and the `Knowledge` fix-it base
  also rendered in TROUBLESHOOTING.md — keep the two in sync.

## Cross-platform notes

Paths resolve per OS (Termux `~/.nimbus-one`, XDG/`~/.config/nimbus-one`,
`%APPDATA%/nimbus-one`). Shell execution picks sh vs PowerShell by
`runtime.GOOS`. mDNS degrades to direct-IP instructions where multicast is
blocked. `/tmp` is never assumed (Termux remaps it) — temp work uses the
data dir and `os.MkdirTemp` defaults.
