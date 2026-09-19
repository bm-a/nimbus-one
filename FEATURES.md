# FEATURES — exhaustive OpenClaw → Nimbus-One map

Every OpenClaw feature observed on-device (`~/.openclaw/openclaw.json`,
386 lines; `workspace-coordinator/`; `extensions/imessage/`), one row each:
status in Nimbus-One, where it lives, what remains. Legend: **done**,
**partial**, **missing**, **out-of-scope** (with reason), **beyond**
(Nimbus-One exceeds OpenClaw).

## A. Gateway & transport

| # | OpenClaw feature (exact key/behavior) | Status | Nimbus-One location / note |
|---|---|---|---|
| A1 | `gateway.mode=local` | **done** | serve runs local; `--no-mdns` flag |
| A2 | `gateway.auth.mode=token` + secret | **done** | bearer token, LAN-bind refusal without one |
| A3 | `gateway.port` / `gateway.bind` | **done** | `http_port`/`http_bind` in config.yaml |
| A4 | `gateway.tailscale.mode` | **missing** | No Tailscale; direct-IP + mDNS documented instead |
| A5 | `gateway.controlUi.sessionObserver` + dashboard assets | **beyond** | Mobile web console `/` + SSE stream + access logging |
| A6 | Startup/crash-loop breakers (stability logs) | **partial** | Panic recovery in broker/engine/main; no crash-loop counter yet |
| A7 | `wizard.lastRun*` onboarding state | **done** | `init --auto` report + `config` wizard |

## B. Telegram channel

| # | OpenClaw feature | Status | Note |
|---|---|---|---|
| B1 | `enabled`, `botToken` | **done** | secrets/env, never logged |
| B2 | `dmPolicy=allowlist` + `allowFrom` + `commands.ownerAllowFrom` | **done** | `Allowed()` on poll + webhook |
| B3 | `dmPolicy=pairing` flow + codes/expiry | **done** | `PairingStore` (6-digit, 10min, single-use, 0600) — needs serve wiring to enforce |
| B4 | `groupPolicy` (open/disabled/allowlist) + `groupAllowFrom` | **done** | `PolicyStore.IsGroupAllowed` — needs serve wiring |
| B5 | Per-group `requireMention/tools.allow/deny/systemPrompt` | **done** | `GroupPolicy` + `ShouldMention` — needs serve wiring |
| B6 | `textChunkLimit` configurable | **done** | `PolicyStore.ChunkLimit()` (default 4000) — needs serve wiring |
| B7 | `streaming.chunkMode/block.coalesce` | **done** | `StreamingSender` (newline/length modes) — needs serve wiring into replies |
| B8 | `heartbeatVisibility` per-channel override | **partial** | Global defaults only; override struct exists in policy, unwired |
| B9 | `healthMonitor.enabled` | **done** | `HealthMonitor` (trip at 3 fails, recover) — needs serve wiring |
| B10 | `actions.reactions` (tapbacks) | **done** | `React()` via setMessageReaction — needs `/react` path or tool |
| B11 | `actions.edit` / `unsend` | **done** | `Edit()` / `Unsend()` — needs tool exposure |
| B12 | Polls | **done** | `SendPoll()` — needs tool exposure |
| B13 | `historyLimit` / `dmHistoryLimit` | **partial** | Session store unbounded; no per-DM cap yet |
| B14 | `mediaMaxMb` / attachments | **partial** | 15–25MB caps in voice paths; no general media config |
| B15 | Voice notes (OpenClaw: whisper script) | **beyond** | Auto-transcribe + `/speak`, no ffmpeg |
| B16 | Long-poll + webhook modes | **done** | `RunPolling` + `ServeWebhook` |

## C. Discord channel (OpenClaw has NONE — all beyond-parity targets)

| # | Feature | Status | Note |
|---|---|---|---|
| C1 | REST send, chunked 2000 | **done** | `Discord.Send` |
| C2 | Inbound WS gateway (HELLO/heartbeat/IDENTIFY/DISPATCH) | **done** | `Gateway.Connect` (hand-rolled RFC6455, stdlib) — wired in serve |
| C3 | Button interactions | **done** | `INTERACTION_CREATE` → `button:<id>` |
| C4 | Thread creation / slash commands | **missing** | Next slice |
| C5 | MESSAGE_CONTENT intent doc | **done** | In code comments |

## D. iMessage (OpenClaw plugin, disabled in live config)

| # | Feature | Status | Note |
|---|---|---|---|
| D1–D12 | dmPolicy/groupPolicy/chunking/streaming/health/actions/media/catchup/accounts | **missing** | Pillar 5: relay bridge to a mac running `imsg`, off by default, untestable on Termux |

## E. Multi-agent roster & delegation

| # | OpenClaw feature | Status | Note |
|---|---|---|---|
| E1 | 4 named persistent agents + `ownership=explicit` | **missing** | Pillar 2: `agents/` registry |
| E2 | Per-agent workspace + agentDir + IDENTITY/SOUL | **missing** | Pillar 2 |
| E3 | Per-agent primary + fallbacks | **missing** | Pillar 2 (pool per agent) |
| E4 | `subagents.allowAgents` + `delegationMode=prefer` | **missing** | Pillar 2 (depth-cap exists, allowlist doesn't) |
| E5 | Main session `agent:<id>:main` (Home) | **missing** | Pillar 2 |
| E6 | `context: isolated/fork` + `visible` | **missing** | Pillar 2 |
| E7 | `sessions_send` / `sessions_yield` | **partial** | `tasks_poll` functionally covers; rename-level |
| E8 | Anonymous subagents + background tasks | **done** | `delegate` + `Tasks` + hourly prune |
| E9 | Single route binding `telegram→coordinator` | **done** | Broker sessions; multi-route comes with roster |

## F. Models / providers / auth

| # | OpenClaw feature | Status | Note |
|---|---|---|---|
| F1 | 7 providers + OpenRouter catalog | **done** | + meta, ollama, deepseek, groq, gemini |
| F2 | Model alias map (13) | **missing** | Pillar 4 |
| F3 | `modelPolicy.allow` enforcement | **missing** | Pillar 4 (warn-only today) |
| F4 | `auth.profiles` + `auth.order` rotation | **missing** | Pillar 4 |
| F5 | Provider metadata (contextWindow/maxTokens/cost/reasoning) | **missing** | Pillar 4 |
| F6 | Experiential provider entry | **missing** | Pillar 4 (trivial) |
| F7 | Deterministic pool (EMA/latency/429-cooldown/401-kill) | **beyond** | Pool + background prober + switch-back |
| F8 | Backoff+jitter, overflow heal, escalation card | **beyond** | resilience.go + TUI modal |

## G. Skills

| # | OpenClaw feature | Status | Note |
|---|---|---|---|
| G1 | SKILL.md 5 format variants | **done** | Tolerant parser |
| G2 | Flat + `category/name` discovery | **done** | `LoadDir` two levels |
| G3 | Script exec + timeout + output cap | **done** | run.sh / sh-fence, 120s, 32KB |
| G4 | fsnotify hot-reload + poll fallback | **done** | With debounce |
| G5 | MCP stdio client | **done** | JSON-RPC, tolerant framing |
| G6 | `skill-card.md` parsed/displayed | **missing** | Pillar 3 |
| G7 | `_meta.json`/origin/lock verification | **missing** | Pillar 3 |
| G8 | ClawHub search/install/update | **missing** | Pillar 3 (network design) |
| G9 | `references/` injected into prompt | **missing** | Pillar 3 (minor) |
| G10 | MCP SSE transport | **missing** | Pillar 3 (stdio done) |

## H. Memory / state

| # | OpenClaw feature | Status | Note |
|---|---|---|---|
| H1 | SOUL/USER/MEMORY/HEARTBEAT flat files | **done** | Atomic tmp+rename |
| H2 | JSONL turns + facts | **done** | Mutex-guarded store |
| H3 | BM25 + vector hybrid recall | **beyond** | RRF fusion, pure Go |
| H4 | Daily `YYYY-MM-DD.md` + timed `HHMM` appends (never overwrite) | **missing** | Pillar 3 |
| H5 | Dreaming light/rem + DREAMS.md markers + session corpus | **missing** | Pillar 3 |
| H6 | Per-skill `state/*.json` | **missing** | Pillar 3 |
| H7 | `hooks.session-memory` | **partial** | Auto-append covers it; no hook bus |

## I. Heartbeat / daemon

| # | OpenClaw feature | Status | Note |
|---|---|---|---|
| I1 | `every`, checklist ticking | **done** | Scheduler + HEARTBEAT.md `- [ ]` |
| I2 | `NO_REPLY` / `HEARTBEAT_OK` quiet protocol | **done** | Filtered in Tick |
| I3 | `lightContext` / `isolatedSession` / `target` | **missing** | Pillar 3 |
| I4 | Quiet hours / per-template checks | **missing** | Pillar 3 |
| I5 | Termux wake-lock + battery throttle | **beyond** | 3x <20%, 6x <10% |
| I6 | mDNS advertise/discover | **beyond** | `_nimbus._tcp`, stdlib |

## J. Security / privacy

| # | Feature | Status | Note |
|---|---|---|---|
| J1 | AES-256-GCM vault + encrypted secrets + 0600/0700 | **beyond** | Auto-created key, env override |
| J2 | Redaction everywhere + env scrubbing | **beyond** | Logs, prompts, bundles, subprocesses |
| J3 | LAN-bind token refusal | **done** | Serve + setup both enforce |
| J4 | Redacted support bundles | **beyond** | Self-checked ZIP |
| J5 | Consent-first self-update + backup | **beyond** | update cmd |

## K. Onboarding / ops / TUI

| # | Feature | Status | Note |
|---|---|---|---|
| K1 | `init --auto` + `auto` detection | **beyond** | Never-overwrite, never-fail-hard |
| K2 | Visual wizard (provider/key/live-ping/order) | **beyond** | bubbletea + headless fallback |
| K3 | `doctor` 13 checks | **beyond** | Panic-isolated, each with fix |
| K4 | `fix` knowledge base (18 topics) | **beyond** | + TROUBLESHOOTING.md mirror |
| K5 | `selftest` 15 fault scenarios | **beyond** | Also in go test |
| K6 | `dashboard` live key health | **beyond** | Pool stats TUI |
| K7 | Universal installers (sh + ps1) | **beyond** | Auto-repair paths |
| K8 | CI + release workflows | **done** | `.github/workflows/` |

## L. Voice / media

| # | Feature | Status | Note |
|---|---|---|---|
| L1 | STT chain (whisper.cpp → endpoint → key → guidance) | **beyond** | No ffmpeg, .ogg as-is |
| L2 | TTS local + API | **beyond** | termux-tts-speak/say/espeak + OpenAI |
| L3 | Telegram voice ↔ `/speak`, web mic/speaker, HTTP endpoints | **beyond** | + agent `transcribe`/`speak` tools |

## Scoreboard

- Done/beyond: ~65 rows. Partial: ~8. Missing (pillars 2–5): ~30.
- Pillar 1 (this session): B3–B12, C2–C3 closed subject to serve-wiring
  follow-ups marked "needs serve wiring" above.
