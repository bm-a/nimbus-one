# Nimbus-One

A minimal coding assistant. One workspace, one model, five tools.

Nimbus-One reads and changes files **only inside one folder you choose**
(the workspace), talks to **one model** (Anthropic Claude), and asks you
to **type "yes" before every shell command**. Nothing else. No channels,
no memory, no database, no scheduling, no telemetry.

## Install

You need [Go](https://go.dev/dl/) 1.24 or newer. Then:

```sh
git clone https://github.com/bm-a/nimbus-one.git
cd nimbus-one
go build -o nimbus-min ./nimbus-min
```

This makes a single file called `nimbus-min`. Put it anywhere you like
(e.g. `~/bin`, or just leave it in the folder).

## Start (first time)

```sh
./nimbus-min onboard
```

It asks three things, in order:

1. **Understand and continue?** — a plain-English summary of what
   Nimbus-One can and cannot do. Type `yes`.
2. **Anthropic API key** — from <https://console.anthropic.com/>.
   It is stored in your config file, readable only by you. Leave it
   empty to use the `ANTHROPIC_API_KEY` environment variable instead.
3. **Workspace folder** — defaults to `~/nimbus-workspace` (created if
   needed). Nimbus-One can **only** touch files inside this folder.

Then try:

```sh
./nimbus-min run "list my files"
```

## Make a request

```sh
./nimbus-min run "add a shopping-list page to my site and test it"
```

Nimbus-One thinks, reads files, makes edits, and — when it needs the
terminal — shows you the exact command plus what it does, then waits
for you to type `yes`. Anything else skips that command. When the task
is done it prints a short summary.

Other commands:

```sh
./nimbus-min serve            # control page + app bridge at 127.0.0.1:8787
./nimbus-min version
./nimbus-min help
```

## Shell confirmations

Before **every** shell command you see:

```
Shell command requested:
  $ go test ./...
  What this does: run the Go tool (test ./...)
Type "yes" to run it, anything else to skip:
```

There is no allowlist, no "remember this command", no bypass flag —
this is deliberate (see `DECISIONS.md`). Without a terminal (for
example inside the app bridge), shell commands are refused outright
instead of running silently.

## The safety boundary

- **Workspace jail:** every file and shell operation is locked to the
  workspace folder. Absolute paths, `..` escapes, and symlink tricks
  are rejected and tested (`go test` proves it).
- **Network:** Nimbus-One only contacts `api.anthropic.com:443`.
  Anything else is blocked in code, not just policy.
- **Undo:** every edit needs exact matching text and reports what
  changed; `git` in your workspace gives full undo (`git diff`,
  `git checkout -- <file>`). We recommend keeping the workspace a git
  repository.

## Troubleshoot

| Problem | Fix |
|---|---|
| `no workspace set` | Run `./nimbus-min onboard` first. |
| `no Anthropic API key` | Re-run `onboard` with a key, or set `ANTHROPIC_API_KEY`. |
| `config file is broken` | Re-run `./nimbus-min onboard` to rebuild it. |
| `path ... escapes the workspace` | Use a path inside the workspace, e.g. `notes/todo.md`. |
| `Anthropic error 401` | Your API key is wrong or revoked — check console.anthropic.com. |
| `Anthropic error 429` | Rate limit — wait a minute and retry. |
| `Anthropic error 529 / 5xx` | Anthropic is having trouble — wait and retry. |
| `stopped after 25 turns` | Break the task into smaller steps. |
| Empty answer | Rephrase the request; the model returned nothing usable. |

## Scope

Nimbus-One intentionally does **not** have: other models, chat apps,
memory across runs, scheduling, remote execution, background agents, a
database, telemetry, or plugins. See `DECISIONS.md` for why, and
`SECURITY.md` for the exact enforcement.
