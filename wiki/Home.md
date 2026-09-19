# Nimbus-One Wiki — Home

Nimbus-One is a minimal coding assistant: **one workspace, one model
(Anthropic Claude), five tools** (read, write, edit, list, shell).
One static binary, pure Go, zero dependencies. Before every shell
command it shows you the exact command and waits for your typed `yes`.

- Your files stay in one folder you choose — nothing outside it is touched.
- The only network contact is `api.anthropic.com:443` (enforced in code).
- No channels, no memory across runs, no database, no telemetry.

> **Scope rule:** if a feature isn't read/write/edit/list/shell
> through the Anthropic loop, Nimbus-One doesn't have it. Anything
> beyond that needs explicit approval — see
> [`nimbus-min/DECISIONS.md`](../nimbus-min/DECISIONS.md).

## Wiki map

- [User Guide](User-Guide.md) — install, onboard, daily `run`, shell approvals, config, troubleshoot.
- [Mobile Termux](Mobile-Termux.md) — phone-first install, footprint, background serve.
- [Operator Guide](Operator-Guide.md) — running `serve`: token, loopback, control page, app bridge.
- [Agent Guide](Agent-Guide.md) — contributing to `nimbus-min/`: layout, jail rules, tests.
- [Advanced Features](Advanced-Features.md) — what exists beyond basics (app protocol) and what's out of scope.
- [FAQ](FAQ.md) — honest answers: cost, privacy, platforms, limits.
- [Voice](Voice.md) — status: not in scope (and why).

Source truth: [`nimbus-min/README.md`](../nimbus-min/README.md),
[`nimbus-min/SECURITY.md`](../nimbus-min/SECURITY.md),
[`nimbus-min/DECISIONS.md`](../nimbus-min/DECISIONS.md).

## 60-second quick start

Paste this whole block (needs `git` + Go 1.24+):

```sh
git clone https://github.com/bm-a/nimbus-one.git
cd nimbus-one
sh nimbus-min/install.sh
./nimbus-min/nimbus-min onboard
./nimbus-min/nimbus-min run "list my files"
```

What each step does:

1. Clone + build (stdlib only — nothing to download).
2. `onboard` — safety summary → Anthropic API key → workspace folder → writes config.
3. `run "list my files"` — first real agent turn through files you own.

Prefer to inspect first? Read [`nimbus-min/install.sh`](../nimbus-min/install.sh)
(it's short and commented). Windows: [`nimbus-min/install.ps1`](../nimbus-min/install.ps1).

Next: [User Guide](User-Guide.md) for daily use,
[Mobile Termux](Mobile-Termux.md) if you are on a phone,
[FAQ](FAQ.md) for honest limits.

## Commands at a glance

`nimbus-min` has five commands. That's all — verify with `help`:

- `onboard` — first-time setup (key, workspace).
- `run "<request>"` — do a coding task in your workspace.
- `serve [addr]` — control page + app bridge (default `127.0.0.1:8787`).
- `version` · `help`

The model inside `run` sees exactly five tools: `read`, `write`,
`edit`, `list`, `shell`.

See also: [FAQ](FAQ.md) · [Operator Guide](Operator-Guide.md).

---
*The legacy prototype tree (`cmd/`, `internal/`, `pkg/`, `docs/`) is
kept in this repo as reference and is not the product. Legacy docs
for it live under `docs/`.*
