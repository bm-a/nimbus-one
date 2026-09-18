# Advanced Features

Beyond daily chat: the systems that make Nimbus-One feel autonomous —
subagents, background work, self-healing keys, the web console, and the
proof harness. See [Agent Guide](Agent-Guide.md) for the cheat-sheet and
[Operator Guide](Operator-Guide.md) for serve hardening.

## Multi-agent delegation

The `delegate` tool spawns a **subagent**: an isolated engine run with its
own scratchpad over the same tools you gave the top agent — nothing more.

- Give a self-contained brief; you get a concise report back (capped 8KB).
- `background: true` returns a task id instead of blocking; poll it with
  `tasks_poll` (`poll` / `list` / `cancel`).
- Depth cap is 2: subagents cannot spawn subagents. They get a refusal
  message, not a silent failure.
- Fan-out pattern: one `delegate` call per independent workstream, then
  synthesize the reports. Heartbeat and HTTP task endpoints keep working
  while subagents run.

## Background tasks

`internal/engine/tasks.go` runs work in goroutines with cancel/list/prune.
`serve` prunes finished tasks older than a day, hourly. The HTTP API's
`POST /api/v1/task` is a second, independent async path (poll with
`GET /api/v1/task?id=`).

## Key self-healing (pool + prober)

- Every key has a state machine: Healthy / Degraded / CoolingDown / Dead.
- 429/503 → CoolingDown until the `Retry-After` / `x-ratelimit-reset`
  timestamp (default 60s). 401/403 → Dead, never silently retried.
- Routing is latency-EMA × in-flight — fastest idle key wins. No tokens
  spent deciding.
- The background **prober** (serve runs it every 60s) token-free checks
  expired cooldowns via `GET {base}/models` and switches healthy keys back
  into rotation with decayed latency. Still-sick keys get extended backoff;
  bad credentials exile to Dead.
- Total failure surfaces as a typed `ExhaustedError` → escalation card
  (new key / switch provider / retry) or, headless, diagnostics plus the
  Nimbus-One route recommendation.

## Context overflow healing

Hitting a context limit doesn't crash the run: the engine compacts the
oldest 30% into a summary marker and resends, up to 2 heals per step.
The 80% sliding compactor prevents most overflows before they happen.

## Web console + SSE

`serve` exposes `/` — a self-contained mobile-first chat console (<14KB,
no CDNs, works on phone browsers over LAN). Token stored in localStorage,
never logged. `GET /api/v1/stream?message=…` streams server-sent events
(one reply event + done, heartbeat comments, auth via Bearer or `?token=`).
CORS origins are configurable; every request is access-logged without
bodies or tokens.

## Error policy: OpenCode or files

Every code-level failure has exactly two paths: delegate to OpenCode
directly (`opencode` tool, headless `run --format json`), or pull the
files and fix them with read/write/bash. Missing sidecar degrades to
guided file instructions — never a crash, never silence.

## Selftest: simulated real-life failures

`nimbus-one selftest` runs 15 hermetic scenarios: 429 storms, 401 exile,
total outage, network flakes, overflow healing, missing sidecar, vault
reload, memory recall, skill execution, HTTP paths, allowlists, LAN scans,
doctor suite, knowledge answers, and prober timer discipline. Same code
runs in `go test`. Before reporting "it doesn't work", run it and paste
the failing scenario.

## Self-update with consent

`nimbus-one update` checks GitHub releases, shows the exact version delta,
asks (unless `--yes`), backs up vault + secrets + workspace + memory to a
timestamped directory, then atomically replaces the binary. Any failure
leaves your data intact and tells you where the backup is. `--repo` /
`NIMBUS_REPO` override the source repo.
