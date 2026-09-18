# Operator Guide

For whoever runs `nimbus-one serve` and keeps it healthy.
Users: [User Guide](User-Guide.md).
Agents: [Agent Guide](Agent-Guide.md).

## Serve hardening

Defaults are local-only: `http_bind 127.0.0.1`,
`http_port 8787`. The binary *refuses* to serve a
non-loopback bind without a token — by design.

```sh
# Local-only (no token needed)
# config.yaml: http_bind: 127.0.0.1
nimbus-one serve

# LAN (token REQUIRED — any one of these)
nimbus-one secrets set http_token <random-value>
NIMBUS_HTTP_TOKEN=<random-value> nimbus-one serve
```

Token lookup for LAN serve: `config` value, else
`http_token` in the secrets store. Missing token +
LAN bind = exit 1 with:

```text
REFUSING to serve LAN without a token
```

Checklist:

1. Bind: keep `127.0.0.1` unless you need LAN.
   (`localhost`, `::1` also count as loopback.)
2. Token: long random value in `http_token`, never in chat.
3. Telegram: always set `TELEGRAM_ALLOW_FROM` to your
   numeric id. Empty allowlist = open bot; `status`
   prints a WARNING.
4. Secrets perms: `vault.key` / `secrets.enc` 0600,
   data dir hardened automatically on bootstrap.
5. Console page (`GET /`) never leaks token state —
   but API calls still enforce `Bearer` auth.

## Backups / restore layout

Data dir contents (see [User Guide](User-Guide.md#setup-paths-compared)
for per-OS paths):

```text
<data-dir>/
  config.yaml      # managed keys (primary_model, fallback_models, ...)
  vault.key        # 0600 — lose this and secrets.enc is unreadable
  secrets.enc      # 0600 — AES-256-GCM store
  workspace/       # SOUL.md USER.md MEMORY.md HEARTBEAT.md (+ your files)
  skills/          # your SKILL.md trees (+ hello starter)
```

`update` backs up automatically (below).
Manual backup is a plain copy — stop `serve` first:

```sh
cp -a ~/.config/nimbus-one ~/.config/nimbus-one.manual-$(date +%F)
```

Restore:

- Full: copy the backup dir back, restart.
- Vault lost: restore the matching `vault.key`.
  No copy? Delete `vault.key` + `secrets.enc` and
  re-run `nimbus-one init --auto` + `nimbus-one config` —
  workspace Markdown is plaintext and survives.
  (Knowledge ID `vault-corrupt`; `nimbus-one fix vault`.)

## Update flow with consent

```sh
nimbus-one update
nimbus-one update --yes
nimbus-one update --repo OWNER/NAME
```

Contract (enforced in code):

1. Check: fetch latest GitHub release tag
   (default placeholder repo `nimbus-one/nimbus-one`
   until first publish; override `--repo` or `NIMBUS_REPO`).
2. Consent: ALWAYS prompt `[y/N]` on a TTY.
   Non-interactive shells MUST pass `--yes` — no silent updates.
3. Backup: `BackupDataDir` copies everything
   (`vault.key`, `secrets.enc`, `workspace/*.md`,
   skills, memories) to `<data-dir>.bak-<timestamp>`.
   Backup failure = refuse to continue.
4. Replace: download `nimbus-one-<goos>-<arch>`
   asset, sanity-check size (>100KB), chmod 0755,
   atomic rename over the running binary.
   Only the binary is touched — never DataDir.
5. Restart the binary yourself to run the new version.

Offline? `update` fails with guidance to retry on
Wi-Fi or build from source. Windows: rename over a
running binary fails — the error tells you to close
nimbus-one and replace the file manually.

## Monitoring via status / dashboard / doctor

```sh
nimbus-one status
nimbus-one dashboard
nimbus-one doctor
nimbus-one doctor --bundle support.zip
```

- `status` (no TTY needed): version, paths, model,
  HTTP bind/port, heartbeat interval, skill/tool counts,
  providers WITH keys (names only, never values),
  Telegram/Discord on/off, plus warnings
  (no keys, Telegram bot open).
- `dashboard` (needs a terminal): live key-health TUI —
  per-key state, latency EMA, in-flight count,
  cooldown seconds. `q` quits. Without a TTY it tells
  you to use `status` instead.
- `doctor`: 13 checks — `go_version`, `data_dir`,
  `vault_key`, `config`, `secrets`, `ollama`,
  `opencode`, `telegram`, `http_port`, `disk`,
  `termux`, `memory`, `skills`. Each failure prints
  a concrete fix. Exit 1 on any failure.
- `doctor --bundle FILE`: writes a REDACTED zip
  (0600) safe to share with a support request.
  Attach it at the printed endpoint
  (`NIMBUS_SUPPORT_URL` or the placeholder
  `https://github.com/<org>/nimbus-one/issues`).
- `fix <symptom>`: searches the 18-topic knowledge base
  first (`nimbus-one fix 429`, `nimbus-one fix telegram`,
  `nimbus-one fix context too long`).
  Full table: [Agent Guide](Agent-Guide.md#knowledge-base-all-18-ids).

Suggested loop: `fix` → `doctor` → `status`/`dashboard`
→ bundle + support endpoint if still stuck.

## Battery / Termux ops

On `serve` under Termux the binary:

- Takes `termux-wake-lock` (released on shutdown).
- Reads `termux-battery-status`; throttles heartbeat:
  below 20% → 3x interval, below 10% → 6x.
  Prints e.g. `battery 15% — heartbeat every 45m`.

Operator advice:

- Disable battery optimization for Termux (app info).
- Prefer foreground or `tmux` sessions for long serves.
- Missing `termux-battery-status` = no throttling, no error.
- Missing wake-lock helper = no-op with nil error.

Full phone playbook: [Mobile Termux](Mobile-Termux.md).

## LAN + mDNS + direct-IP fallback

Advertise (automatic on `serve` unless `--no-mdns`):

- Service `_nimbus._tcp`, instance = hostname,
  TXT `mode=<mode> v=<version>`, port = your `http_port`.

Discover:

```sh
nimbus-one discover
```

5-second scan. Empty results are NORMAL on hotspots /
guest Wi-Fi (multicast blocked) — nothing is broken.

Fallback (always works):

1. Find your LAN IP (`ip addr`, or the `lan:` line in
   `init --auto` / `auto` output).
2. Bind LAN + set token (see hardening above).
3. Share `http://<your-lan-ip>:8787` + the bearer token.
4. Clients call `/api/v1/chat` or open `/` console.

Skip multicast entirely when you know the network
blocks it: `nimbus-one serve --no-mdns`.

Related: [User Guide](User-Guide.md#everyday-flows) ·
[Mobile Termux](Mobile-Termux.md) · [FAQ](FAQ.md).
