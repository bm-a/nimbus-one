# Mobile Termux

Phone-first Nimbus-One on Android + Termux.
General use: [User Guide](User-Guide.md).

## Install (Termux) — one paste

```sh
pkg install golang git
git clone https://github.com/bm-a/nimbus-one.git
cd nimbus-one
sh nimbus-min/install.sh
./nimbus-min/nimbus-min onboard
```

Notes:

- First `go build` on a phone takes a few minutes; later builds are incremental.
- The binary is ~10MB, stdlib only — no Node, Python, or Chromium to install.
- Data dir: `~/.nimbus-one`
  (i.e. `/data/data/com.termux/files/home/.nimbus-one`).
- The `install.sh` script detects Termux and prints the right Go install hint if missing.

## Footprint

- Binary ~10MB; config + workspace are kilobytes.
- `go build` needs toolchain space during build (~hundreds of MB);
  reclaim with `pkg clean` / `go clean -cache`.
- No background services, no daemons — `run` starts, works, exits.

## Keeping `serve` alive

Android freezes background apps. For long serves:

1. Keep a foreground session (or `tmux` / `termux-services`).
2. Android Settings → Apps → Termux → Battery → Unrestricted.
3. `termux-wake-lock` while serving (needs the Termux:API app from F-Droid).

```sh
termux-wake-lock
./nimbus-min/nimbus-min serve
```

Then open `http://127.0.0.1:8787` — on the phone itself. For another
device, bind LAN explicitly (`serve 0.0.0.0:8787`) with the token
from your config — see [Operator Guide](Operator-Guide.md).

## Troubleshooting on phones

| Symptom | Fix |
|---|---|
| Serve dies in background | Foreground session + battery unrestricted + wake-lock. |
| `go: command not found` | `pkg install golang`, then re-run install. |
| Slow first build | Normal on ARM; one-time cost. |
| Keystrokes eaten during `yes` prompt | Use a foreground terminal, not a background session. |

Related: [User Guide](User-Guide.md) · [FAQ](FAQ.md).
