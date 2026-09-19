# Nimbus-One

A minimal coding assistant. One workspace, five tools, your choice of
provider.

Nimbus-One reads and changes files **only inside one folder you choose**
(the workspace), talks to **one provider at a time** (46 supported —
Anthropic, OpenAI, DeepSeek, local Ollama, and more; see `models`),
and asks you to **type "yes" before every shell command**. No channels,
no memory, no database, no scheduling, no telemetry.

## Install — one copy-paste

You need `git` + [Go](https://go.dev/dl/) 1.24 or newer. Paste this
whole block:

```sh
git clone https://github.com/bm-a/nimbus-one.git
cd nimbus-one
sh nimbus-min/install.sh
./nimbus-min/nimbus-min onboard
./nimbus-min/nimbus-min run "list my files"
```

The script clones (or updates), builds (stdlib only — nothing to
download), and offers first-run setup. Prefer to inspect first? Read
[`install.sh`](install.sh) — it's short and commented. Windows:
[`install.ps1`](install.ps1). Manual build instead:

```sh
cd nimbus-min
CGO_ENABLED=0 go build -o nimbus-min .
```

This makes a single file called `nimbus-min` (~10MB). Put it anywhere
you like (e.g. `~/bin`, or just leave it in the folder).

## Start (first time)

```sh
./nimbus-min onboard
```

It asks, in order:

1. **Understand and continue?** — a plain-English summary of what
   Nimbus-One can and cannot do. Type `yes`.
2. **Provider** — numbered shortlist (or any id from `nimbus-min models`).
3. **Model** — default shown; typed only when the provider has none.
4. **API key** — stored in your config file, readable only by you.
   Empty = use the provider's env var (shown). Local providers skip this.
5. **Workspace folder** — defaults to `~/nimbus-workspace` (created if
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
./nimbus-min run --provider deepseek "fix it"   # one-off override (also --model, --base-url)
./nimbus-min models            # all 46 providers, key envs, defaults (* = yours)
./nimbus-min serve            # control page + app bridge at 127.0.0.1:8787
./nimbus-min version
```

Provider, model, and base URL resolve flags → `NIMBUS_PROVIDER` /
`NIMBUS_MODEL` / `NIMBUS_BASE_URL` env → config → provider default.
Keys resolve config → provider env vars → `NIMBUS_API_KEY`.
Custom servers (vLLM, SGLang, LiteLLM, any key in an odd env var):
`--base-url URL` with the matching provider shape.

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
- **Network:** Nimbus-One only contacts its configured provider
  (loopback for local ones). Anything else is blocked in code,
  not just policy — see `SECURITY.md`.
- **Undo:** every edit needs exact matching text and reports what
  changed; `git` in your workspace gives full undo (`git diff`,
  `git checkout -- <file>`). We recommend keeping the workspace a git
  repository.

## Troubleshoot

| Problem | Fix |
|---|---|
| `no workspace set` | Run `./nimbus-min onboard` first. |
| `no API key for X` | Re-run `onboard` with a key, or set the named env var. |
| `unknown provider` | Run `nimbus-min models` for the exact ids. |
| `has no default model` | Set one via `onboard`, config `model`, or `--model`. |
| `config file is broken` | Re-run `./nimbus-min onboard` to rebuild it. |
| `path ... escapes the workspace` | Use a path inside the workspace, e.g. `notes/todo.md`. |
| `rejected the API key (401)` | Key wrong or revoked — check your provider dashboard. |
| `rate limit (429)` | Wait a minute and retry. |
| `having trouble (5xx)` | Provider-side — wait and retry. |
| `stopped after 25 turns` | Break the task into smaller steps. |
| Empty answer | Rephrase the request; the model returned nothing usable. |

## Scope

Nimbus-One intentionally does **not** have: chat apps,
memory across runs, scheduling, remote execution, background agents, a
database, telemetry, or plugins. Providers beyond the table
(SDK-auth clouds, OAuth CLIs) are excluded with reasons in
`DECISIONS.md`. See that file for why, and `SECURITY.md` for the
exact enforcement.
