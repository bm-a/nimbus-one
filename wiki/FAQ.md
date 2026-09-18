# FAQ

Short honest answers. Details: [User Guide](User-Guide.md) ·
[Operator Guide](Operator-Guide.md) ·
[Agent Guide](Agent-Guide.md) · [Mobile Termux](Mobile-Termux.md).

> **Suggestion vs your choice:** the Muse route
> (`meta/muse-spark-1.3-contributor` via OpenRouter)
> is a *recommendation*. Fallbacks are *your choice* —
> nothing is applied until you confirm it in
> `nimbus-one config`, `fallback_models`, or
> `NIMBUS_FALLBACK_MODELS`.

## 1. What is Nimbus One?

A single-binary autonomous agent runtime in pure Go.
Chat, serve, Telegram/Discord, heartbeat, skills, LAN
discovery — without Node.js, Python, or Chromium.

## 2. How is it different from OpenClaw / Hermes?

Weight. OpenClaw/Hermes-class stacks need multiple
runtimes and heavier hardware. Nimbus One is one static
binary (`CGO_ENABLED=0`) that runs on any Android phone,
laptop, or server. Ideas referenced, never copied
(see `ATTRIBUTION.md`).

## 3. Which commands actually exist?

`init auto config run exec serve skills secrets models
discover doctor update fix status dashboard version help`.
Verify: `nimbus-one help [command]`.
Cheat-sheet with every real flag:
[Agent Guide](Agent-Guide.md#command-cheat-sheet).

## 4. What flags do `run` / `serve` / `update` take?

Only these (verified in `main.go` — nothing else exists):

- `run|exec`: `--mode plan|build`, `--model M`.
- `serve`: `--mode ...`, `--no-mdns`.
- `init`: `--auto`. `doctor`: `--bundle FILE`.
  `update`: `--yes`, `--repo OWNER/NAME`.
- There is no `--port` / `--bind` / `--token` CLI flag —
  those are `config.yaml` keys (`http_port`, `http_bind`).

## 5. How do fallbacks work? Is anything automatic?

You choose the full order: primary, then your
`fallback_models` in your order. The pool routes by
latency + key health across YOUR keys only.
Suggestions (starting with the Muse route) are shown,
never applied. Empty fallbacks = primary only.

## 6. What is the Muse route, exactly?

*Suggestion:* `meta/muse-spark-1.3-contributor` via the
OpenRouter free tier (or a direct Meta endpoint).
Shown in the wizard, `nimbus-one models`, and the
all-keys-exhausted escalation card. Adopt it by
confirming in `config` — or ignore it freely.

## 7. Does it work with no API keys?

Yes — no-keys mode. With no keys and no Ollama you get
clear guidance (`nimbus-one config` or local Ollama),
not a silent failure. With Ollama opted in, local-only
mode works fully offline. (`llm-nokeys`.)

## 8. What does it cost?

Nimbus One itself: free (MIT). Model costs are yours:
free tiers (OpenRouter free models, Ollama local = free),
then per-token provider billing. No token is ever spent
deciding where to route; cost discipline (free/local
first) is in the default `SOUL.md`.

## 9. Is my data private?

Local-first: state, history, skills on your disk.
Secrets AES-256-GCM encrypted, redacted from every log
and prompt, scrubbed from subprocess env. LAN serve
refuses without a token. Telegram needs an allowlist.
Support bundles redact by default. No telemetry, accounts,
or callbacks.

## 10. Windows / macOS supported?

Yes. Data dir: `%APPDATA%/Nimbus One` (Windows),
`~/.config/nimbus-one` (macOS/Linux),
`~/.nimbus-one` (Termux). Shell picks sh vs PowerShell
by `runtime.GOOS`. Two Windows caveats: secret prompt
echoes (warning shown), and `update` cannot replace a
running binary — close it and swap the file manually.

## 11. Can I lose data on update?

Update is consent-first + backup-first: prompt (or
`--yes` non-interactive), full copy to
`<data-dir>.bak-<timestamp>` (vault, secrets, workspace,
memories), then atomic binary replace. Backup failure =
refuse to update. Your data is never touched by the
replace step itself.

## 12. How do updates ask for consent?

`nimbus-one update` prints the version delta and asks
`Proceed? [y/N]` on a TTY. Non-TTY requires `--yes`
explicitly. Nothing updates silently, ever.

## 13. Telegram bot answers strangers — help?

Expected with an empty allowlist. Set
`TELEGRAM_ALLOW_FROM=<your-numeric-id>` (ask @userinfobot),
restart serve. (`tg-open`.)

## 14. `discover` finds no peers — broken?

No. Hotspots and guest Wi-Fi usually block multicast.
Serve still works via direct IP: share
`http://<your-lan-ip>:8787` + bearer token. (`mdns-none`.)

## 15. Context-too-long / rate-limit / 401 errors?

- Overflow: auto-compacts oldest 30%, resends ≤2x. If
  recurrent, summarize instead of pasting dumps.
- 429: traffic already moved to your next key; hot key
  rejoins after `Retry-After`. Add a second key.
- 401: key marked Dead, never retried — replace via `config`.
- Start with `nimbus-one fix <symptom>`, then `doctor`.
  All 18 topics: [Agent Guide](Agent-Guide.md#knowledge-base-all-18-ids).

## 16. Plan vs build — which when?

`plan` = read-only inspection (safe default for audits,
unknown repos, expensive questions). `build` = full tools
(default, does the work). Habit: plan first, build second
for anything destructive. Examples:
[User Guide](User-Guide.md#plan-vs-build-with-examples).

## 17. How do I write a skill in 10 lines?

Minimal `skills/<name>/SKILL.md` with `name` +
`description` frontmatter (triggers optional, body =
instructions). Strict fields optional; unknown keys
ignored. Format + strict example:
[Agent Guide](Agent-Guide.md#skills-format).

## 18. Where do I file issues / get help?

Run `nimbus-one fix <symptom>` then `nimbus-one doctor`.
Still stuck: `nimbus-one doctor --bundle support.zip`
and attach it at the printed endpoint (`NIMBUS_SUPPORT_URL`
or the placeholder `https://github.com/<org>/nimbus-one/issues`).
