# SECURITY — Nimbus-One

Exact enforcement, not aspirations. Every claim below names the file
and has a test proving it (`go test ./...`).

## 1. Workspace jail (`jail.go`)

- The workspace root is resolved once at startup (`initJail`):
  absolute + `EvalSymlinks` + must be a directory.
- Every file tool (`tools.go`: read, write, edit, list) passes through
  `resolve()`, which rejects:
  - empty paths and NUL bytes,
  - **absolute paths** (even ones pointing inside the workspace —
    callers must use workspace-relative paths, no exceptions),
  - `..` escapes after `filepath.Clean`,
  - **symlink escapes**: existing paths are fully resolved; missing
    paths resolve the longest existing ancestor plus remainder, then
    re-check containment. A symlink inside the workspace pointing
    outside is rejected for both reads and writes.
- Shell commands pass `checkShellText()` before anything runs:
  absolute-path tokens, `..` segments, and `cd /…` escapes are
  rejected. Quoting cannot hide a path (the scanner splits on quotes
  and operators, checking every piece).
- The shell process itself starts with its working directory locked to
  the workspace root (`cmd.Dir = workspaceRoot`), so relative access
  stays inside even if a check missed something.

Tests: `jail_test.go` (`../../etc/passwd`, nested traversal, absolute
rejection, symlink-out rejected / symlink-in allowed, outward-symlinked
parent dir for writes) and `tools_test.go` (escape attempts on every
tool, shell cwd lock via `pwd`, failure/timeout behavior).

## 2. Shell confirmation (`confirm.go`)

- `confirmShell` is the single chokepoint: exact command + one-line
  plain-English explanation, then require exactly `yes`
  (case-insensitive, trimmed). Anything else refuses.
- There is **no pre-approval list, no allowlist, no bypass flag** —
  by your explicit correction, not by omission.
- Headless (no terminal, piped stdin, app bridge): refuse with
  guidance to re-run in a terminal. Never execute, never queue.
- `rm` explanations warn explicitly that deletion cannot be undone.

Tests: `confirm_test.go` (yes/YES approve; `y`, empty, `no`,
`yes yes` refuse; headless refuses even with `yes` piped in).

## 3. Network boundary (`netguard.go`, `providers.go`)

- The program's only HTTP client construction (`guardedHTTPClientFor`)
  pins the dialer to **exactly one `host:port`: the active provider's**.
  Local providers pin loopback (`127.0.0.1:11434`, …). No aliases, no
  wildcards, no second client.
- This covers telemetry, update checks, logging, analytics: they are
  impossible by construction — there is no other dial path.
  (`grep -rn "http.Client{" --include=*.go` shows one site.)
- Switching providers switches the pin: the Anthropic key cannot be
  sent to DeepSeek's host because that host is undialable while
  Anthropic is active, and vice versa. Keys are per-provider by
  construction, not by discipline.
- Custom `--base-url` values pin to their own host the same way; a
  malformed base fails closed to anthropic-only.

Tests: `netguard_test.go` (example.com, wrong scheme, lookalike
subdomain all blocked).

## 4. Model containment (`loop.go`)

- The model never touches disk or shell directly. It emits `tool_use`
  blocks; `executeToolCall` validates the name (unknown tools rejected
  with the five valid names) and routes through the jail + confirm
  gate above.
- Malformed input (missing name, bad JSON, empty answer) is an error
  with guidance, never a silent skip or a crash.
- Tool errors return to the model as `tool_result` with `error:`
  text, so it can recover; the loop caps at 25 turns.

Tests: `loop_test.go` (unknown tool, nameless block, HTTP 401, empty
answer, max-turn cutoff, shell yes/headless paths).

## 5. Secrets

- The API key lives in the config file (`0600`, owner-only), the
  provider's env vars, or `NIMBUS_API_KEY`. It is sent only as the
  auth header to the pinned provider host — and the pin means a key
  can never leak to a different provider's host (see §3).
- Keys never appear in logs, errors, or transcripts. Error paths
  redact bodies to one line (`oneLine`, 300 chars).

## 6. App bridge (`compat.go`)

- The bridge binds a local address (default `127.0.0.1:8787` —
  loopback, not LAN) and requires the minted token when one exists.
- Only three methods exist: `connect`, `chat.send`, `sessions.list`.
  Everything else answers `UNKNOWN_METHOD` — nothing is faked.
- `chat.send` runs headless, so shell calls inside it hit the same
  mandatory refusal as any headless run.

## Known limits (honest, not hidden)

- The shell scanner is conservative, not a full shell parser: exotic
  constructs (`$VAR` expansions resolving to `/`, command substitution
  producing paths) are partially covered by the locked `cmd.Dir` and
  the confirm gate, but a determined user typing `yes` to a malicious
  command the model proposed can still do harm *inside the workspace*.
  The gate guarantees informed consent, not omniscience.
- `rm` inside the workspace is allowed after `yes` — undo is via the
  user's own git, which onboard recommends but cannot enforce.
- No sandboxing beyond the workspace jail (no containers, no separate
  OS user). The threat model is a confused or mistaken model, not a
  hostile local attacker.
