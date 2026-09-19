# HANDOFF — compaction handoff for the NEXT agent session

Date: 2026-09-19 · Branch: `main` · Remote: `https://github.com/bm-a/nimbus-one`
Read this file first, then `AGENTS.md`. It replaces the compacted chat history.

---

## 1. What this project is + version state

Nimbus-One is a single-binary autonomous agent runtime in pure Go
(`CGO_ENABLED=0` always) — the lightweight replacement for
OpenClaw/Hermes-class stacks. No Node.js, Python, Chromium, or new hardware:
runs on any Android phone (Termux), laptop, or server. One binary provides the
ReAct engine (plan/build modes), provider pool with deterministic failover,
skills + MCP, Telegram/Discord/HTTP channels, heartbeat daemon, voice, and
diagnostics. Local-first (Markdown state, JSONL history, encrypted secrets);
no telemetry, no accounts, no callbacks.

**Version state: `0.1.0-beta`** (legacy tree) + **`nimbus-min 0.1.0`**
(the product — see §10). Root README fronts `nimbus-min/`; the legacy
`cmd/nimbus-one` tree is prototype reference. Eleven commits on `main`.
Working tree clean at handoff time.

---

## 2. Repo map — every directory + what lives there

**Root files:** `AGENTS.md` (agent contract — read before changing anything),
`README.md` (quick start, command table, layout), `ATTRIBUTION.md`,
`CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `FEATURES.md` (UNTRACKED —
exhaustive OpenClaw→Nimbus-One map, done/partial/missing/out-of-scope/beyond),
`Makefile` (`build`, `cross`, …), `install.sh` / `install.ps1` (explicit manual
steps only — no pipe-to-shell), `go.mod` / `go.sum` (module `nimbus-one`,
Go 1.24.2, pure-Go deps only: bubbletea/lipgloss/bubbles/fsnotify),
`nimbus-one` (UNTRACKED local build artifact — do NOT commit), `dist/`
(release artifacts), `.gitignore`, `.github/workflows/` (`ci.yml`,
`release.yml`), `LICENSE` (MIT).

- `cmd/nimbus-one/main.go` (~1490 lines) — the entire CLI: `init [--auto]`,
  `auto`, `config`, `run`/`exec [--mode plan|build] [--model M]`, `serve
  [--mode …] [--no-mdns]`, `skills`, `secrets set|get|del|list`, `models`,
  `discover`, `doctor [--bundle FILE]`, `update [--yes] [--repo O/N]`,
  `fix <symptom>`, `selftest`, `speak <text>`, `transcribe <file>`,
  `status`/`dashboard`, `version`, `help`. Also `bootstrap()` (config→vault→
  workspace→skills→provider pool→engine), `buildProvider()` (user's primary +
  THEIR fallback order + opt-in Ollama; never silent), `escalate()` (TTY card
  vs headless diagnostics). Full per-command reference: `docs/COMMANDS.md`.
- `internal/config/` — `Config` load/save, per-OS data-dir resolution
  (Termux `~/.nimbus-one`, XDG/`~/.config/nimbus-one`, `%APPDATA%`), ports,
  model order, allowlists.
- `internal/llm/` — provider clients (OpenAI-compatible SSE, Anthropic,
  Gemini, Ollama, OpenCode sidecar), `resilience.go`
  (jittered backoff cap 5s, `ContextOverflowError`, `ExhaustedError`),
  `openrouter.go` catalog + `SuggestedModels` (labeled, inert until confirmed),
  `pool/` — per-key state machine (Healthy/Degraded/CoolingDown/Dead),
  latency-EMA routing, `Retry-After` parsing, 401-kill, 5xx circuit-break,
  deterministic tier failover, background `StartProber`.
- `internal/engine/` — ReAct loop (≤25 steps, 3 retries/tool w/ feedback),
  process-wide atomic plan/build modes, 80% sliding compactor (oldest 30%,
  resend ≤2x), `BuildPrompt`, subagent fan-out (`DelegateTool`, depth cap),
  background `Tasks`/`TasksTool` (+ hourly prune in serve), escalation types.
- `internal/state/` — `Workspace` (SOUL/USER/MEMORY/HEARTBEAT.md),
  `JSONLStore` turns/facts, `Memory` BM25 + hashed-vector hybrid recall
  (reciprocal-rank fusion). Swappable `Store` interface, no Cgo DB.
- `internal/skills/` — tolerant SKILL.md parser (OpenClaw-minimal →
  Hermes-strict), shell/script exec w/ timeouts, `RegisterAll`,
  fsnotify `Watcher` w/ polling fallback (serve hot-reload).
- `pkg/mcp/` — `client.go` + `protocol.go`: MCP stdio client + tests.
- `internal/tools/` — bash/fs/web builtins behind the `Tool` interface;
  workspace-relative paths; `Registry`.
- `internal/gateway/` — `broker.go` (one Broker, sessions isolated per
  channel/user), `session.go`, `repl.go`, `telegram.go` (long-polling,
  allowlist-gated), **`telegram_policy.go` (NEW, untracked)**,
  **`telegram_actions.go` (NEW, untracked)**, `discord.go` (REST sender),
  **`discord_ws.go` (NEW, untracked)**, `http.go` (REST API `:8787` + bearer
  token + LAN-bind refusal), `webui.go` (mobile console `/` + SSE + access
  log), `voice.go` (Telegram/web voice wiring), plus `*_test.go` per file.
- `internal/daemon/` — `Heartbeat` ticker (HEARTBEAT.md checklist),
  `Scheduler`, Termux wake-lock + battery throttling (<20%→3x, <10%→6x),
  `ParseEvery`/`ThrottleForBattery`.
- `internal/secure/` — vault (AES-256-GCM, 0600 key), encrypted secrets store
  (`secrets.enc` 0600), `Redact` (all logs/prompts/errors), `HardenDataDir`
  (0700/0600), subprocess env scrubbing, `KnownKeys()`.
- `internal/setup/` — `Detect` (Ollama/OpenCode/keys/LAN) + `Apply`
  (never-overwrite) + embedded `DefaultWorkspace()`/`DefaultHelloSkill`;
  `templates_test.go` fails on drift with `default_workspace/`.
- `internal/netdiscover/` — mDNS `_nimbus._tcp` advertise + 5s browse;
  degrades to direct-IP guidance where multicast is blocked.
- `internal/doctor/` — 13 checks, redacted support bundles
  (`Bundle` + `SupportEndpoint`), `Knowledge` fix-it base (18 topics,
  mirrored in TROUBLESHOOTING.md — keep in sync), `Find()`.
- `internal/selftest/` — 15-scenario hermetic fault simulation
  (dead keys, 429 storms, outages, overflow, missing binaries, corrupt
  vaults, skill runs, HTTP paths, telegram gates, LAN scans, doctor, KB).
- `internal/update/` — consent-first self-update (`LatestRelease`,
  `NeedsUpdate`, `BackupDataDir`, atomic replace; refuses without TTY
  unless `--yes`).
- `internal/tui/` — bubbletea wizard (`RunWizard`), health dashboard
  (`RunDashboard`), escalation modal (`RunEscalation`). Only non-stdlib UI.
- `internal/media/` — STT (whisper.cpp → `NIMBUS_STT_URL` → OpenAI Whisper;
  `.ogg` as-is, no ffmpeg) + TTS (termux-tts-speak/say/espeak);
  `speak`/`transcribe` commands + HTTP + Telegram voice-note paths.
- `internal/log/` — leveled logger (`NIMBUS_LOG_LEVEL`), redaction-aware.
- `default_workspace/` — canonical SOUL/USER/MEMORY/HEARTBEAT.md + `hello`
  skill; binary embeds copies; drift breaks `templates_test.go`.
- `docs/` — `COMMANDS.md` (full operator reference + env-var table),
  `ARCHITECTURE.md` (request-flow diagram, subsystem + cross-platform notes),
  `TROUBLESHOOTING.md` (mirrors doctor `Knowledge`).
- `wiki/` — `Home.md` (start here) + `User-Guide`, `Operator-Guide`,
  `Agent-Guide`, `Mobile-Termux`, `Advanced-Features`, `Voice`, `FAQ`.
- `agents/` — **DOES NOT EXIST YET** (`ls` confirmed). Reserved for future
  OpenCode subagent definitions; AGENTS.md forbids inventing files there
  unasked. Creating it is pillar-2 work (§4).
- `.github/workflows/` — `ci.yml` (vet+test+build, README badge live),
  `release.yml` (tagged releases + checksums).

---

## 3. DONE (mega-build — all verified green)

### Pillar 1 — messaging (committed)
Policy/Health/Streaming libs + `React/Edit/Unsend/SendPoll` +
`PairingStore` + stdlib Discord WS `Gateway.Connect`, all **wired in
`serve`**: group/mention gating + health-tracked sends in `handleUpdate`,
Discord gateway connects on boot with REST fallback. Follow-ups inside
pillar-1 follow-up commit (see git log): Telegram `OffsetFile`
persistence, 429 retry-after, typing indicators, voice-reply chunk via
policy, `GroupContext`, callbacks/polls/edited routing, `/help /reset`
commands, `TrimSession`, Discord threads/edits/guild-routes/BaseURL
override. `FEATURES.md`: exhaustive 100+ row OpenClaw map (A–L).

- `internal/gateway/telegram_policy.go` (252 lines, +test): `PolicyStore`
  (group gating `open|disabled|allowlist` + `GroupAllow`; per-group
  `GroupPolicy{RequireMention, AllowedTools, DeniedTools, SystemPrompt}`;
  `IsGroupAllowed` — DMs always allowed; `GroupConfig`, `ChunkLimit`
  default 4000, `ShouldMention`); `StreamingSender` (length|newline chunk
  modes, `Write`/`Flush`); `HealthMonitor` (`RecordSuccess`/`RecordFailure`/
  `IsHealthy`). Telegram struct owned elsewhere — policy consulted via hooks.
- `internal/gateway/telegram_actions.go` (334 lines, +test): `React`, `Edit`,
  `Unsend`, `SendPoll` via bounded `postTelegramMethod` (15s client, JSON,
  truncated error bodies, empty-token guard); `PairingStore` (6-digit codes,
  10-min expiry, single-use, 0600 file). FEATURES.md B3/B4/B5/B6 note these
  still **need serve wiring to enforce** — check whether that happened.
- `internal/gateway/discord_ws.go` (865 lines, +test): stdlib-only RFC6455
  Discord Gateway — `Gateway.Connect` (GET `/gateway/bot` → handshake →
  HELLO(10) → heartbeat loop → IDENTIFY(2) → DISPATCH(0)); `MESSAGE_CREATE`
  + `INTERACTION_CREATE` routed via `Broker.Handle("discord",…)` with REST
  reply; reconnect w/ backoff+jitter (`wsMaxAttempts`), ctx wins;
  `MESSAGE_CONTENT` (1<<15) + GUILD_MESSAGES/DM intents documented
  (empty-content events skipped honestly). **Wired in `serve`**
  (`cmd/nimbus-one/main.go` ~987-995: `&gateway.Gateway{…}` + `Connect` in
  goroutine, REST-send fallback warning, non-fatal).
### Mega-build depth layers (same session, same gates)

- **Agents:** NEW `internal/agents/` — roster (`agents.yaml`, entries/list
  forms, ownership, default resolution, `AgentSelectionRequired`),
  session keys (`agent:<id>:main`, parse/classify), alias index +
  allowlist (exact + `provider/*`, empty=deny, `AllowAll()`).
- **Sessions/memory:** `internal/state/sessions.go` (JSONL session store,
  lifecycle fencing, fork, archive), `flush.go` (threshold-margin +
  dated files), `dreaming.go` (light dedup / deep promote / REM
  patterns, pure), `provenance.go` (sha256 sidecars, fail-closed).
  Daemon `heartbeat2.go` (phase-hash due, active hours, cooldown,
  run keys, empty-content detector, visibility precedence).
- **Engine/skills/hooks:** `send/yield/wait/structured` tools,
  `ask_user` with timeout/cancel/primary-gate, child context modes
  (isolated/fork/light) wired into `DelegateTool`, `RunStream`
  (delta streaming + degrade), skill tiers + cards + profiles,
  NEW `internal/hooks` bus (fan-out, recover, family prefix).
- **LLM:** `usage.go` (multi-provider normalize + accumulator),
  `pricing.go` (16-model table, unknown=0), `windows.go` (18-entry
  catalog + warn/block guard), `authprofiles.go` (order/pin/cooldown/
  exile/env-enumeration), `chain.go` (dedupe + skip-cache + origins),
  `Chunk.Usage` capture in client (stream + non-stream).
- **Gateway+:** WS JSON-RPC server (hijack RFC6455, ping/chat/status/
  sessions.list), OpenAI-compat (`/v1/models`, `/v1/chat/completions`,
  honest 400 on stream), role scopes, config `Watcher` primitive.
- **Tools:** `edit` (exact-once + receipts), `process` (bg sessions +
  cursors), paged read + gitignore + `re:` regex + context lines,
  mutation queue, Brave→Tavily→Exa→DDG chain, MCP SSE client + MCP
  stdio server, NEW `internal/browser` (CDP over hand-rolled WS,
  external Chrome only), sandbox drift audit, Termux computer
  (screencap/tap/swipe).
- **Cron/nettail:** NEW `internal/cron` (5-field parser), NEW
  `internal/nettail` (tailscale serve/funnel exec + node registry).
- **NOT yet wired in `main.go`:** agents roster load, session store
  swap-in, hooks triggers, WS server start, OpenAI routes register,
  tools profile flag, cron/tailscale/nodes serve flags, usage/cost
  surfacing. That wiring is the FIRST job (§6).

### Prior work (committed, `git log --oneline -8` shows 5)

1. `14995e2` — Nimbus One 0.1.0: single-binary runtime (engine, pool+prober,
   skills+MCP, memory, tools, gateway, daemon, secure, setup, TUI).
2. `29d2947` — universal installers, workspace-relative tool paths, model
   grounding, live-run fixes.
3. `277e968` — beta: display-name unification, installers, grounding fixes.
4. `04270d2` — CI + release automation, README badge.
5. `1d4cb60` — first-run hardening: vault/doctor unification, voice stack
   (STT/TTS + Telegram + HTTP + web console), env grounding note, debug tap.
   Plus along the way: multi-agent primitives (`DelegateTool`/`Tasks`,
   depth cap, hourly prune), `selftest` 15/15, consent-first `update`,
   `fix` KB (18 topics), mDNS, LAN-token refusal, redacted bundles.

---

## 4. NEXT IN ORDER (first job is wiring, then leftovers)

**FIRST — wire the mega-build into `main.go`/`serve`:** load agents
roster (`agents.yaml` or default), session store for broker history,
hooks triggers (message lifecycle, compact, gateway start/stop),
WS server + OpenAI route registration, tools profile env
(`NIMBUS_TOOLS_PROFILE`), cron-driven heartbeat option, tailscale
serve/funnel flags, node registry commands, usage/cost in
`status`/logs. Keep every new surface behind user-confirmed config
(convention 1). Then update FEATURES.md scoreboard rows that changed.

**Leftover gaps (from 5 source-dives, still open):** plugin
contract/registry (intentional divergence — stdlib single binary, do
NOT build unless user demands), Tailscale funnel publish (exec exists,
serve flag missing — covered above), ClawHub registry client
(search/install/verify — network design decision needed),
Browser CDP needs external Chrome (documented), computer-use beyond
Termux (macOS/Linux screenshot backends), STT realtime streaming,
Pool.Chat mid-stream failover (Chain has it, Pool picks once),
per-group tool scoping enforcement (`GroupContext` exposes, engine
doesn't consume), `StreamingSender`/`React/Edit` unwired to flows,
voice-reply chunk ignores policy limit, offset file path unwired
(`OffsetFile` field exists, serve doesn't set it), MCP OAuth,
sandbox containers (by design: containment only).

**Pillars 2–5 libraries: DONE this session (see §3 Mega-build).**
What remains is WIRING (§4 FIRST) + leftovers. Do not re-plan them.

---

## 5. Conventions that MUST be preserved

1. **User chooses, never silent defaults.** No hardcoded fallback chains; pool
   failover only across the user's own keys/tiers; suggestions
   (`SuggestedModels`, `models` cmd) labeled + inert until confirmed in
   `config` or `fallback_models`. New pillars (agents, model policy) inherit
   this: allowlists/profiles are user-confirmed or absent.
2. **Stdlib-first + existing pure-Go deps only.** `go.mod` may use
   bubbletea/lipgloss/bubbles/fsnotify (already vendored). No Cgo, no new
   daemons/runtimes, no new network deps (discord_ws proves RFC6455 by hand).
3. **No stubs.** No TODO/placeholder/dead code. Untestable paths (iMessage on
   Termux) degrade with an explicit warning, never silently.
4. **`NIMBUS_` env prefix** for all new env vars
   (see `docs/COMMANDS.md` §Environment for the current table — extend it).
5. **Module path `nimbus-one`** (bare, `go.mod:1`). Import as
   `nimbus-one/internal/…`.
6. **Workspace-relative tool paths.** Relative paths resolve inside the
   workspace; the env-note in the system prompt says so — keep `envNote()`
   accurate when tools change.
7. **Secrets never logged.** Every error/log/prompt path through
   `secure.Redact`; tests use fake keys; secret files 0600, dirs 0700;
   `secrets list` shows names only.
8. **plan/build modes.** Process-wide atomic; `plan` hides + blocks mutating
   tools. New tools/agents must declare their mode correctly.
9. Cross-platform: every OS call needs a `runtime.GOOS` branch or documented
   degradation; never assume `/tmp` (Termux remaps — use data dir /
   `os.MkdirTemp` defaults). No pipe-to-shell installers (explicit steps).
   Doctor `Knowledge` ↔ `TROUBLESHOOTING.md` stay in sync.

---

## 6. Verification ritual (after EVERY change)

```sh
go vet ./... && go test -count=1 ./... && CGO_ENABLED=0 go build ./...
./nimbus-one selftest          # expect 15/15 PASS
make cross                     # before claiming portability
```

Plus a **real-binary smoke test** of any touched command with an isolated
data dir, e.g. `NIMBUS_DATA_DIR=$(mktemp -d) ./nimbus-one doctor` /
`… init --auto` / `… run "hello"` (env override works — proven in this
history). Fix real failures — never weaken a test.
First job next session: the §4 wiring (roster load, session store,
hooks, WS+OpenAI routes, tools profile, cron/tailscale/nodes flags,
usage surfacing), then FEATURES.md scoreboard refresh, then commit.

---

## 7. Credentials hygiene

- **GitHub PATs were pasted in chat history — rotate them if live.**
  Treat any token that appeared in chat as compromised.
- Remote is clean: `origin https://github.com/bm-a/nimbus-one.git`
  (fetch+push), no credentials in `git config --list` (only `user.name/
  user.email`, `branch.main.*`). Nothing secret in the repo (vault key,
  `secrets.enc`, data dir are git-ignored / outside the tree).
- Support bundles (`doctor --bundle`) are redacted by construction — safe to
  attach to issues. Never paste tokens into chat again; use
  `nimbus-one secrets set <key>` (encrypted at rest) or env vars.

## 8. Open questions for the user

(a) ClawHub registry client (search/install/verify): build or skip?
(b) iMessage: still last/disabled-by-default? (c) Plugin contract/registry:
confirm intentional divergence stays. (d) OpenRouter credits are exhausted
(HTTP 402 on live runs) — real-model emulation of Phase 2 long sessions,
Phase 3 delegation, and git-tool flows is pending credits.

## 9. Platform rebuild, Phases 0–3 (2026-09-20, commits to `8411644`)

Direction change: NOT an OpenClaw rewrite — a Go-native coding-first
runtime (OpenCode loop concepts + OpenClaw reach concepts). All green:
vet/test/selftest 15/15/cross ×5, pushed.

- Phase 0 (`402c9bf`): `internal/perms` triples (allow/ask/deny,
  last-match-wins, fail-closed), `internal/session` SQLite via
  `modernc.org/sqlite v1.46.0` (pinned go1.24 set: x/sys v0.38.0,
  libc v1.67.6), modes as ruleset presets, run persists turns.
- Phase 1 (`272e444`): `internal/prompt` env+variants, `internal/llm`
  ModelProfile table (gpt→patch shape), `internal/vcs` git awareness,
  `internal/verify` post-edit checkers in edit/patch/write receipts,
  `PatchTool` envelope, `GitTool`, `internal/context` spillover in bash,
  jail on opencode-dir + transcribe-path, whitespace-trim path fix.
- Phase 2 (`01e9cd4`): context estimator + Budget states, prune old tool
  outputs, summary-compaction with extractive fallback, verbose skills,
  402 billing error made actionable.
- Phase 3 (`8411644`): delegate agent kinds (explore read-only by
  construction), `llm.RouteModel` + config `routes:`, engine hooks
  (session.start/tool.after, observe-only), cron registry + tool,
  `session.Lanes`, routes in status, COMMANDS.md agent-tools section.
- Emulator findings fixed: stray-space ENOENT, raw 402 JSON dump.
- Deferred (documented in code): child session persistence, cron SQLite
  + daemon execution, serve/broker lanes wiring, git worktrees, dynamic
  task descriptions, official MCP SDK, tsnet/chromedp.

## 10. Nimbus-One minimal runtime (2026-09-20, commit `64d57a2`)

New direction, user-approved spec: minimal Anthropic-only agent for a
non-programmer. Built in `nimbus-min/` (own Go module, stdlib only,
zero deps) — legacy tree untouched. All green: gofmt/vet/test,
`CGO_ENABLED=0` build, clean-clone rebuild + retest, pushed.

- Scope lock: 5 tools (read/write/edit/list/shell), 1 workspace,
  Anthropic-only, no persistence/channels/memory/DB/scheduling.
  Anything outside needs explicit approval.
- Jail (`jail.go`): relative-only paths, `..`/absolute/NUL rejected,
  symlink-escape defense both directions, shell-text scanner +
  locked `cmd.Dir`. Bugs found in testing: ancestor-walk dropped a
  segment (fixed), quoted absolute paths slipped the first scanner
  (scanner rewritten to split on quotes/operators).
- Confirm gate (`confirm.go`): mandatory typed-`yes`, NO allowlists
  (user correction), headless refuses with guidance.
- Loop (`loop.go`/`anthropic.go`): tool_use→validate→execute→result,
  25-turn cap, malformed/empty/401 handling; `netguard.go` dials only
  api.anthropic.com:443.
- App compat (`compat.go`, hand-rolled RFC6455): connect→hello-ok
  (protocol 4), chat.send, sessions.list; everything else
  UNKNOWN_METHOD (pairing/Tailscale/nodes NOT faked). Green control
  page served at `/`. Live-socket verified (auth, hello-ok,
  list, no-key AGENT_FAILED).
- Onboard wizard (OpenClaw order, minimal): consent → key → workspace
  → `0600` config + minted token. Unknown config fields rejected.
- Docs: `nimbus-min/README.md` (non-programmer), `SECURITY.md`
  (exact enforcement + honest limits), `DECISIONS.md` (9 trade-offs).
- Emulator (fresh user, real binary): onboard, no-key/broken-config/
  unknown-command errors, serve health/page/WS — all correct.
- NOT done: live Anthropic turn (user runs it with their key —
  fake-server loop tests cover the path); legacy-tree Phases 2–3
  live emulation still pending credits (see §8d).
- Repo: root README fronts `nimbus-min/` as the product; CI has a
  `min` job; no secrets/artifacts committed.
