# Operator Guide

For whoever runs `nimbus-min serve` and keeps it healthy.
Users: [User Guide](User-Guide.md).

## What `serve` does

One command, three surfaces on one local address
(default `127.0.0.1:8787`):

- `GET /` — green control page (connect, chat, session list; vanilla JS, no CDNs).
- `GET /healthz` — `{"ok":true,"product":"nimbus-min"}`.
- `/ws` — app bridge: `connect` → `chat.send` → `sessions.list` (protocol 4).

```sh
nimbus-min serve                # 127.0.0.1:8787
nimbus-min serve 0.0.0.0:8787   # LAN — token required (below)
```

## Token + bind rules

- The token is minted by `onboard` into `nimbus.json` (`http_token`).
- Loopback binds need no token; LAN binds should use the token in the
  control page / app `connect` (`auth: {token}`).
- Wrong token → `UNAUTHENTICATED`. Old protocol (`maxProtocol < 4`) → `VERSION_MISMATCH`.
- There is no TLS — put a reverse proxy in front if you expose it beyond your LAN.

## Headless shell policy

`chat.send` runs the agent **headless**: shell calls are refused
with guidance, never executed. This is the same mandatory gate as
the terminal (see [`nimbus-min/DECISIONS.md`](../nimbus-min/DECISIONS.md)),
not a second policy. If remote shell execution is ever needed, it
arrives as an explicit, approved feature — not a reinterpretation.

## What's NOT here

No Telegram/Discord, no heartbeat scheduler, no wake-lock
management, no mDNS, no TLS, no user accounts. The bridge is a
single-user local service. Anything beyond that is out of scope
until explicitly approved.

## Backups

Copy two things: your **workspace folder** and `nimbus.json`.
That is the entire state — there is no database. Stop `serve`
first, copy, done.
