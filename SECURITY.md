# Security policy

## Scope

Nimbus One is local-first: keys live encrypted on your disk, traffic goes
only to providers you configured. The attack surface that matters: the
HTTP API, Telegram/Discord bots, skill scripts, and MCP servers.

## Hardening already in place

- Bearer tokens required for non-loopback HTTP binds (refused otherwise).
- Telegram allowlist (`TELEGRAM_ALLOW_FROM`); Discord bot token auth.
- Secrets encrypted at rest (AES-256-GCM, 0600 files, 0700 dirs),
  redacted from logs/prompts/bundles, scrubbed from subprocess env.
- Skill scripts run with timeouts and directory scoping; `plan` mode
  blocks all mutation.

## Reporting a vulnerability

Email the maintainer (see GitHub repo contact) with `nimbus-one doctor
--bundle support.zip` attached — bundles are redacted by default. Do NOT
open public issues for vulnerabilities. Expect acknowledgment within a
week and a fix release with a `CHANGELOG.md` entry crediting you
(unless you prefer anonymity).

## What not to report

- Missing allowlist warnings (by design, loudly warned).
- `doctor` failing without providers configured (guidance, not a bug).
- Upstream provider outages (check `nimbus-one status` first).
