# Nimbus-One

[![CI](https://github.com/bm-a/nimbus-one/actions/workflows/ci.yml/badge.svg)](https://github.com/bm-a/nimbus-one/actions)

A minimal coding assistant. **One workspace, one model, five tools.**
Nimbus-One reads and changes files only inside one folder you choose,
talks only to Anthropic's servers, and asks you to type **"yes"**
before every shell command. Nothing else — no channels, no memory,
no database, no telemetry. One static binary, pure Go, zero
dependencies.

## Install — one copy-paste

Needs: `git` + Go 1.24+ (`pkg install golang git` on Termux,
https://go.dev/dl/ elsewhere). Then paste this whole block:

```sh
git clone https://github.com/bm-a/nimbus-one.git
cd nimbus-one
sh nimbus-min/install.sh
./nimbus-min/nimbus-min onboard
./nimbus-min/nimbus-min run "list my files"
```

What it does: clones the repo, builds the binary (stdlib only —
nothing to download), runs first-time setup (API key + workspace
folder), then does a first task. Prefer to inspect first? Read
[`nimbus-min/install.sh`](nimbus-min/install.sh) — it's short and
commented. Windows: [`nimbus-min/install.ps1`](nimbus-min/install.ps1).
No pipe-to-shell, ever.

## Use

```sh
nimbus-min onboard          # first-time setup (key, workspace)
nimbus-min run "fix the typo in notes/todo.md"
nimbus-min serve            # control page + app bridge at 127.0.0.1:8787
nimbus-min version
```

Before **every** shell command you see the exact command, a
plain-English explanation, and a prompt for typed `yes`. Anything
else skips it. Outside a terminal, shell commands are refused rather
than run silently.

## Safety in one paragraph

Every file and shell operation is locked to your workspace folder:
absolute paths, `..` escapes, and symlink tricks are rejected (and
tested — `go test` proves it). The only network contact is
`api.anthropic.com:443`, enforced in code. Your key lives in a
`0600` config file or `ANTHROPIC_API_KEY`. Full details:
[`nimbus-min/SECURITY.md`](nimbus-min/SECURITY.md); every
safety-vs-convenience trade-off: [`nimbus-min/DECISIONS.md`](nimbus-min/DECISIONS.md).

## Docs

- [`nimbus-min/README.md`](nimbus-min/README.md) — install, use, troubleshoot (start here)
- [`wiki/Home.md`](wiki/Home.md) — wiki map + 60-second start
- [`wiki/User-Guide.md`](wiki/User-Guide.md) · [`wiki/FAQ.md`](wiki/FAQ.md) · [`wiki/Mobile-Termux.md`](wiki/Mobile-Termux.md)
- [`wiki/Operator-Guide.md`](wiki/Operator-Guide.md) — running `serve`
- [`wiki/Agent-Guide.md`](wiki/Agent-Guide.md) — contributing to `nimbus-min/`

## What this repo also contains

`nimbus-min/` is the product. The rest (`cmd/`, `internal/`, `pkg/`,
`docs/`) is the prior full-scope prototype tree — kept as reference,
not the product. It is covered by its own CI job; `nimbus-min` builds
and tests independently (`cd nimbus-min && go test ./...`).

Ideas referenced (never copied) from OpenClaw, OpenCode, and others —
see `ATTRIBUTION.md`. MIT licensed (`LICENSE`).
