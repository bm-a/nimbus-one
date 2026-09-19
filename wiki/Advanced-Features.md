# Advanced Features

Beyond daily `run`: what else exists, and what deliberately doesn't.
Basics: [User Guide](User-Guide.md).

## App bridge protocol

`serve` speaks a tested subset of the OpenClaw gateway protocol
(frames `{type:req,id,method,params}`, protocol 4):

| Method | Behavior |
|---|---|
| `connect` | Token auth → `hello-ok` (methods, events, policy). Bad token → `UNAUTHENTICATED`; old client → `VERSION_MISMATCH`. |
| `chat.send {text}` | One headless agent turn → `res{reply, sessionId}` + `session.message` event. Shell inside is refused (headless gate). |
| `sessions.list` | In-memory session log of this process. |
| anything else | `UNKNOWN_METHOD` — honest, not faked. |

Wire details: `nimbus-min/compat.go` (hand-rolled RFC6455, stdlib
only). Tests: `compat_test.go` (frames, auth, flow) plus a live
socket handshake verified against the running server.

## Custom servers

Any OpenAI-compatible server works without a table row: pass
`--base-url URL` (or `NIMBUS_BASE_URL`) paired with the matching
provider shape, key via `NIMBUS_API_KEY`. This covers vLLM, SGLang,
LiteLLM, proxies, and keys living in unconventional env vars. The
network pin follows the override host automatically.

## Explicitly out of scope

Each is a maintenance + security surface the product promise
("small enough to audit") excludes. They return only if explicitly
approved, with tests and docs — never smuggled in via "compatibility":

- Fallback chains / multi-key routing (one provider per run; `--provider` switches explicitly)
- SDK-auth clouds, OAuth CLIs, ambiguous endpoints (see `models` footer for the list + reasons)
- Chat channels (Telegram, Discord, …)
- Memory across runs, databases, history search
- Scheduling, cron, heartbeat automation
- Remote execution, background agents, nodes
- Voice (see [Voice](Voice.md))
- Plugins, skills marketplace, MCP servers
- Telemetry, analytics, update checks, accounts

Rationale per item: [`nimbus-min/DECISIONS.md`](../nimbus-min/DECISIONS.md).
