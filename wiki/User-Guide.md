# User Guide

For humans using Nimbus-One daily. Phones: [Mobile Termux](Mobile-Termux.md).
Running `serve`: [Operator Guide](Operator-Guide.md).
Contributing: [Agent Guide](Agent-Guide.md).

## Install + onboard

One paste (needs `git` + Go 1.24+):

```sh
git clone https://github.com/bm-a/nimbus-one.git
cd nimbus-one
sh nimbus-min/install.sh
./nimbus-min/nimbus-min onboard
./nimbus-min/nimbus-min run "list my files"
```

`onboard` asks, in order: safety summary (`yes` to continue) →
Anthropic API key (from https://console.anthropic.com/, empty = use
`ANTHROPIC_API_KEY` env) → workspace folder (default
`~/nimbus-workspace`, created if needed). Then it writes the config
and mints an HTTP token for the app bridge.

Where things live:

- Termux: `~/.nimbus-one`
- Linux/macOS: `~/.config/nimbus-one` (or `$XDG_CONFIG_HOME/nimbus-one`)
- Windows: `%APPDATA%/nimbus-one`
- Inside: `nimbus.json` (`0600` — workspace, optional key, HTTP token)

## Daily use

```sh
nimbus-min run "fix the typo in notes/todo.md"
nimbus-min run "add input validation to app.py and test it"
```

The loop (max 25 turns): model thinks → calls a tool → result feeds
back → repeat until it answers with a summary. The model sees exactly
five tools: `read`, `write`, `edit`, `list`, `shell`.

`edit` needs search text occurring **exactly once** in the file.
On mismatch you (via the model) get a numbered snippet — read it and
retry with more surrounding lines.

## Shell approvals

Before **every** shell command:

```
Shell command requested:
  $ go test ./...
  What this does: run the Go tool (test ./...)
Type "yes" to run it, anything else to skip:
```

There is no allowlist, no "remember", no bypass — deliberately (see
[`nimbus-min/DECISIONS.md`](../nimbus-min/DECISIONS.md)). `rm`
warnings say deletion cannot be undone. Without a terminal, shell
commands are refused with guidance instead of running silently.

Tip: keep shell commands small and reversible; `git` in your
workspace gives full undo (`git diff`, `git checkout -- <file>`).

## Config

`nimbus.json` has three keys: `api_key` (optional if
`ANTHROPIC_API_KEY` is set), `workspace`, `http_token`.
Unknown keys are rejected (typo protection); a broken file tells you
to re-run `onboard`. There is nothing else to configure — that is
the point.

## Troubleshoot

| Problem | Fix |
|---|---|
| `no workspace set` | Run `nimbus-min onboard`. |
| `no Anthropic API key` | Re-run `onboard` with a key, or set `ANTHROPIC_API_KEY`. |
| `config file is broken` | Re-run `onboard` to rebuild it. |
| `path ... escapes the workspace` | Use a path inside the workspace, e.g. `notes/todo.md` (never absolute). |
| `Anthropic error 401` | Key wrong or revoked — check console.anthropic.com. |
| `Anthropic error 429` | Rate limit — wait a minute, retry. |
| `Anthropic error 5xx` | Anthropic is struggling — wait, retry. |
| `stopped after 25 turns` | Split the task into smaller steps. |
| Empty answer | Rephrase; the model returned nothing usable. |

Related: [FAQ](FAQ.md) · [Operator Guide](Operator-Guide.md).
