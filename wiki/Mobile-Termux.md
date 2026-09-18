# Mobile Termux

Phone-first operations for Android + Termux.
General setup: [User Guide](User-Guide.md).
Hardening + monitoring: [Operator Guide](Operator-Guide.md).

## Install (Termux)

Explicit steps only — no pipe-to-shell installer,
per repo policy (`AGENTS.md` rule 7).

```sh
pkg update
pkg install golang git
git clone https://github.com/<you>/nimbus-one.git
cd nimbus-one
go build -o nimbus-one ./cmd/nimbus-one
./nimbus-one init --auto
./nimbus-one config
./nimbus-one run "hello"
./nimbus-one serve
```

Notes:

- First `go build` on a phone takes a few minutes; later
  builds are incremental.
- `init --auto` prints a `lan:` line — your direct-IP
  fallback when mDNS is blocked (see
  [Operator Guide](Operator-Guide.md#lan--mdns--direct-ip-fallback)).
- Data dir on Termux: `~/.nimbus-one`
  (i.e. `/data/data/com.termux/files/home/.nimbus-one`).

## Storage / RAM footprint expectations

Nimbus One is one static binary, no runtimes:

- No Node.js, Python, or Chromium to install.
- State is flat files: Markdown (`SOUL/USER/MEMORY/HEARTBEAT`),
  JSONL turns/facts — kilobytes, growing slowly.
- `go build` needs Go toolchain space (~hundreds of MB
  during build); the resulting binary is tens of MB.
- Reclaim: `pkg clean`, `go clean -cache` (slower next build).
- If disk is tight, `nimbus-one doctor` includes a disk
  write-probe check that fails early with a concrete fix.

RAM: the agent loop is one process; heavy work is
delegated to providers (cloud) or Ollama (local = heavy).
On low-RAM phones prefer provider keys over local models.

## Wake-lock

Android freezes background apps. On `serve`, Nimbus One
calls `termux-wake-lock` automatically and releases it
(`termux-wake-unlock`) on shutdown. Missing helper = no-op.

For long serves, do all three:

```sh
termux-wake-lock
nimbus-one serve
```

1. Keep a foreground session (or `tmux` / `termux-services`).
2. Android Settings → Apps → Termux → Battery →
   Unrestricted / Don't optimize.
3. Acquire `termux-wake-lock` (automatic on serve, but
   re-acquire after reboots; `termux-wake-unlock` to release).

Symptom `termux-wake` → `nimbus-one fix wake`.

## Battery throttling table

Source: `daemon.ThrottleForBattery` + `termux-battery-status`.
Base interval = `heartbeat_every` (`15m` default).

| Battery | Heartbeat interval |
|---|---|
| 20% or more | base (e.g. every 15m) |
| Below 20% | 3x base (e.g. every 45m) |
| Below 10% | 6x base (e.g. every 90m) |

- `serve` prints e.g. `battery 15% — heartbeat every 45m`.
- `termux-battery-status` missing/unparseable = no
  throttling, no error spam.
- Tune base: `heartbeat_every: 30m` in `config.yaml`.

## Termux:API optionals

All optional — Nimbus One degrades with warnings, never
silent no-ops:

- `termux-wake-lock` / `termux-wake-unlock` — keep serving.
- `termux-battery-status` — battery-aware heartbeat.
- Without Termux:API installed, serve still works;
  `doctor` reports the `termux` check accordingly.

Install: `pkg install termux-api` plus the Termux:API
app from F-Droid (Play Store builds are stale).
Grant battery-status permission when prompted.

## Offline Ollama vs keys: cost / latency tradeoffs

Your choice — Nimbus One applies nothing by itself.

| Route | Cost | Latency / quality | Needs |
|---|---|---|---|
| Provider keys (suggested: OpenRouter + `meta/muse-spark-1.3-contributor` free tier) | Free tier, then per-token | Fast, best quality; needs network | `nimbus-one config`, paste key, confirm fallback order |
| Local Ollama | Free, on-device, no key | Slower on phones, smaller models; fully offline | `ollama serve` + `ollama pull llama3.1`, or `OLLAMA_MODEL` / model name opt-in |
| No backend | Free | Guidance only, no completions | `nimbus-one fix no backend` |

Rules from code:

- Ollama joins the pool only when you opt in
  (model name like `llama* qwen* mistral* mixtral* phi* gemma*`
  / `ollama*` in primary/fallbacks, or `OLLAMA_BASE_URL` /
  `OLLAMA_HOST` / `OLLAMA_MODEL` set). Otherwise silent.
- Entries without a usable key are skipped with a warning.
- `nimbus-one models` works offline via the built-in
  suggestion list — labeled, never applied.
- On Android without Ollama, provider keys are the
  recommended path (see `ollama-down` fix text).

## Troubleshooting phone-specific failures

```sh
nimbus-one fix wake
nimbus-one fix ollama
nimbus-one fix telegram
nimbus-one fix no backend
nimbus-one doctor
nimbus-one doctor --bundle support.zip
```

| Symptom | Likely cause → fix |
|---|---|
| Serve dies in background | Android freeze → wake-lock + battery unrestricted + foreground/tmux. |
| `battery` line missing | `termux-battery-status` absent → install Termux:API or ignore (no throttling). |
| Heartbeat too frequent / rare | `heartbeat_every` value → set Go duration (`15m`, `1h`) in `config.yaml`. |
| `discover` finds nothing | Hotspot/guest Wi-Fi blocks multicast → use direct IP (`lan:` line + token). |
| Telegram silent | Token wrong (`tg-401`) or allowlist unset (`tg-open`) → `secrets set telegram`, `TELEGRAM_ALLOW_FROM=<id>`. |
| LAN serve refused | By design (`http-lan-token`) → `secrets set http_token <random>` or bind `127.0.0.1`. |
| Keys exhausted on mobile data | 429s on free tiers → add second key, spread providers, watch `status` (no-TTy safe) instead of `dashboard`. |
| Update fails offline | Retry on Wi-Fi or build from source (`git pull` + `go build`). |

Related: [User Guide](User-Guide.md) ·
[Operator Guide](Operator-Guide.md) · [FAQ](FAQ.md).
