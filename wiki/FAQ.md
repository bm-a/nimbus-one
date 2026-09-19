# FAQ

Short honest answers. Details: [User Guide](User-Guide.md) ·
[Operator Guide](Operator-Guide.md) · [Mobile Termux](Mobile-Termux.md).

## 1. What is Nimbus-One?

A minimal coding assistant: one workspace, one model (Anthropic
Claude), five tools (read, write, edit, list, shell). One static
binary, pure Go, zero dependencies.

## 2. How is it different from OpenClaw?

Scope. OpenClaw is a platform (channels, plugins, nodes, cron).
Nimbus-One is one loop: your request → Claude → tools in your folder
→ done. Ideas referenced, never copied (see `ATTRIBUTION.md`).

## 3. Which commands exist?

Five: `onboard`, `run`, `serve`, `version`, `help`. Verify:
`nimbus-min help`. The model inside `run` sees five tools:
`read write edit list shell`. Nothing else exists.

## 4. What does it cost?

Nimbus-One: free (MIT). Claude usage: your Anthropic billing
(pay-per-token; check console.anthropic.com). The 25-turn cap bounds
worst-case cost per request; keep tasks small.

## 5. Is my data private?

Workspace files stay on your disk. The key lives in a `0600` config
file or env var, sent only to `api.anthropic.com` — the only host
the binary can contact (enforced in code, not policy). No telemetry,
no accounts, no callbacks. Prompts necessarily include the files the
model reads; that is the product working as designed.

## 6. Windows / macOS supported?

Yes — pure Go, no CGO. Config dir: `%APPDATA%/nimbus-one`
(Windows), `~/.config/nimbus-one` (macOS/Linux), `~/.nimbus-one`
(Termux). Install via `nimbus-min/install.ps1` on Windows.

## 7. Does it work with no API key?

No — and it says so plainly (`no Anthropic API key — run onboard…`).
One model, one key, no offline mode. This is an explicit scope
decision, not a missing feature.

## 8. Can shell commands hurt me?

They run as you, inside your workspace, after your typed `yes`.
`rm` after `yes` really deletes — undo is via your own git, which
onboard recommends but cannot enforce. Read each prompt; keep
commands small. See [`nimbus-min/SECURITY.md`](../nimbus-min/SECURITY.md).

## 9. Why no channels / memory / scheduling / voice?

Out of scope by design. Each is a maintenance and security surface
for a product whose promise is "small enough to audit". See
[`nimbus-min/DECISIONS.md`](../nimbus-min/DECISIONS.md) §7 and
[Voice](Voice.md), [Advanced Features](Advanced-Features.md).

## 10. Plan vs build modes?

There are none. Every run has full tools; safety comes from the
workspace jail + the mandatory `yes` gate, not from modes.

## 11. Where do I file issues?

At https://github.com/bm-a/nimbus-one/issues — include the exact
command, the full error text, and your OS/Go version. Never paste
your API key.
