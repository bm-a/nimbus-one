#!/bin/sh
# Nimbus-One installer — Termux / Linux / macOS.
# Read this file first, then run ONE of:
#   sh nimbus-min/install.sh                 # clone + build, asks before changing anything
#   sh nimbus-min/install.sh --yes            # same, no prompts
#   sh nimbus-min/install.sh --local          # build from this checkout, no clone
# Flags: --yes --dir DIR --repo OWNER/NAME --skip-go --no-onboard --local -h
# Idempotent: safe to re-run; it rebuilds instead of duplicating.
set -u

REPO="${NIMBUS_REPO:-bm-a/nimbus-one}"
DIR="${NIMBUS_DIR:-$HOME/nimbus-one}"
YES=0; SKIP_GO=0; NO_ONBOARD=0; LOCAL=0

for a in "$@"; do
  case "$a" in
    --yes) YES=1 ;;
    --skip-go) SKIP_GO=1 ;;
    --no-onboard) NO_ONBOARD=1 ;;
    --local) LOCAL=1 ;;
    --dir=*) DIR="${a#--dir=}" ;;
    --repo=*) REPO="${a#--repo=}" ;;
    --dir|--repo) NEEDVAL="$a" ;;
    -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
    *) if [ -n "${NEEDVAL:-}" ]; then
         case "$NEEDVAL" in
           --dir) DIR="$a" ;;
           --repo) REPO="$a" ;;
         esac
         NEEDVAL=""
       else
         echo "unknown flag: $a (see --help)" >&2; exit 2
       fi ;;
  esac
done
[ -n "${NEEDVAL:-}" ] && { echo "usage: $NEEDVAL VALUE" >&2; exit 2; }

info() { printf '[nimbus] %s\n' "$*"; }
die() { printf '[nimbus] FATAL: %s\n' "$*" >&2; exit 1; }
ask() {
  if [ "$YES" = 1 ]; then return 0; fi
  printf '[nimbus] %s [y/N]: ' "$1" >&2
  read -r ans || return 1
  case "$ans" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}
have() { command -v "$1" >/dev/null 2>&1; }

# --- 1. git + go ---
have git || die "git not found — install it first (Termux: pkg install git), then re-run"
if ! have go; then
  if [ "$SKIP_GO" = 1 ]; then die "go not found and --skip-go set — nothing to build with";
  fi
  info "go not found. Install it, then re-run:"
  case "$(uname -s 2>/dev/null)" in
    Linux*)
      if [ -n "${TERMUX_VERSION:-}" ] || [ -d /data/data/com.termux/files/usr ]; then
        echo "  pkg install golang"
      else
        echo "  https://go.dev/dl/ (1.24+), or: sudo apt install golang"
      fi ;;
    Darwin*) echo "  brew install go   (or https://go.dev/dl/)" ;;
    *) echo "  https://go.dev/dl/ (1.24+)" ;;
  esac
  exit 1
fi

# --- 2. sources ---
if [ "$LOCAL" = 1 ]; then
  [ -f nimbus-min/go.mod ] || die "--local needs the repo root (nimbus-min/go.mod not here)"
  SRC="$PWD"
  info "using local checkout: $SRC"
else
  if [ -f "$DIR/nimbus-min/go.mod" ]; then
    info "existing checkout at $DIR — updating"
    (cd "$DIR" && git pull --ff-only) || warn=" (pull failed, building as-is)"
    info "building as-is${warn:-}"
  else
    ask "clone https://github.com/$REPO into $DIR?" || die "cancelled — nothing changed"
    git clone "https://github.com/$REPO" "$DIR" || die "clone failed"
  fi
  SRC="$DIR"
fi

# --- 3. build (stdlib only, no network beyond the module cache) ---
info "building nimbus-min (pure Go, no dependencies)..."
(cd "$SRC/nimbus-min" && CGO_ENABLED=0 go build -trimpath -o nimbus-min .) || die "build failed"
BIN="$SRC/nimbus-min/nimbus-min"
info "built: $BIN"
"$BIN" version || die "built binary does not run"

# --- 4. onboard ---
if [ "$NO_ONBOARD" = 0 ]; then
  if [ -t 0 ]; then
    info "starting first-run setup..."
    "$BIN" onboard || warn="onboard did not finish — run '$BIN onboard' later"
    [ -n "${warn:-}" ] && info "$warn"
  else
    info "no terminal — skipping onboard. Run this when ready:"
    info "  $BIN onboard"
  fi
fi

info "done. Next:"
info "  $BIN run \"list my files\""
info "  $BIN serve   # control page at http://127.0.0.1:8787"
