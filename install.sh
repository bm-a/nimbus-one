#!/bin/sh
# Nimbus One universal installer — Termux / Linux / macOS.
# Inspect first, then run:  sh install.sh [--yes] [--dir DIR] [--repo OWNER/NAME] [--skip-go] [--no-init]
# No blind pipes: download this file (or clone the repo) and read it before executing.
# Idempotent: safe to re-run; it repairs instead of duplicating.
set -u

REPO="${NIMBUS_REPO:-bm-a/nimbus-one}"
DIR="${NIMBUS_DIR:-$HOME/nimbus-one}"
YES=0; SKIP_GO=0; NO_INIT=0; LOCAL=0

for a in "$@"; do
  case "$a" in
    --yes) YES=1 ;;
    --skip-go) SKIP_GO=1 ;;
    --no-init) NO_INIT=1 ;;
    --local) LOCAL=1 ;;
    --dir=*) DIR="${a#--dir=}" ;;
    --repo=*) REPO="${a#--repo=}" ;;
    --dir|--repo) echo "usage: $a VALUE" >&2; exit 2 ;;
    -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
    *) echo "unknown flag: $a (see --help)" >&2; exit 2 ;;
  esac
done

info() { printf '[nimbus] %s\n' "$*"; }
warn() { printf '[nimbus] WARNING: %s\n' "$*" >&2; }
die() { printf '[nimbus] FATAL: %s\n' "$*" >&2; exit 1; }
ask() {
  if [ "$YES" = 1 ]; then return 0; fi
  printf '[nimbus] %s [y/N]: ' "$1" >&2
  read -r ans || return 1
  case "$ans" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

# --- 1. OS / arch detect ---
OS="unknown"; ARCH="$(uname -m 2>/dev/null || echo unknown)"
case "$(uname -s 2>/dev/null)" in
  Linux*)  OS="linux" ;;
  Darwin*) OS="darwin" ;;
esac
if [ -n "${TERMUX_VERSION:-}" ] || [ -d "/data/data/com.termux/files/usr" ]; then
  OS="termux"
fi
info "detected: $OS / $ARCH"

GOARCH=""
case "$ARCH" in
  aarch64|arm64) GOARCH="arm64" ;;
  x86_64|amd64) GOARCH="amd64" ;;
  *) die "unsupported arch $ARCH — install Go 1.22+ manually from https://go.dev/dl/, then: git clone https://github.com/$REPO.git && cd nimbus-one && go build -o nimbus-one ./cmd/nimbus-one" ;;
esac

# --- 2. Helpers: package managers ---
have() { command -v "$1" >/dev/null 2>&1; }
try_install() {
  # $1 = package name; tries every manager present. Returns 0 on success.
  pkg="$1"
  if [ "$OS" = "termux" ] && have pkg && pkg install -y "$pkg" 2>/dev/null; then return 0; fi
  if have apt-get && (apt-get install -y "$pkg" 2>/dev/null || sudo apt-get install -y "$pkg" 2>/dev/null); then return 0; fi
  if have dnf && (dnf install -y "$pkg" 2>/dev/null || sudo dnf install -y "$pkg" 2>/dev/null); then return 0; fi
  if have apk && (apk add "$pkg" 2>/dev/null || sudo apk add "$pkg" 2>/dev/null); then return 0; fi
  if have brew && brew install "$pkg" 2>/dev/null; then return 0; fi
  if have pacman && (pacman -S --noconfirm "$pkg" 2>/dev/null || sudo pacman -S --noconfirm "$pkg" 2>/dev/null); then return 0; fi
  return 1
}

# --- 3. git + curl (fetch tools) ---
for tool in git curl; do
  if ! have "$tool"; then
    info "installing missing tool: $tool"
    try_install "$tool" || die "could not install $tool — install it manually, then re-run: sh $0"
  fi
done

# --- 4. Go toolchain (>= 1.22) ---
go_ok() {
  have go || return 1
  ver="$(go version 2>/dev/null | sed -E 's/.*go([0-9]+)\.([0-9]+).*/\1 \2/' )" || return 1
  # shellcheck disable=SC2086
  set -- $ver
  [ "${1:-0}" -gt 1 ] || { [ "${1:-0}" -eq 1 ] && [ "${2:-0}" -ge 22 ]; }
}
install_go_tarball() {
  for v in 1.23.4 1.22.12; do
    case "$OS" in
      termux) file="go${v}.android-arm64.tar.gz" ;;
      *)      file="go${v}.${OS}-${GOARCH}.tar.gz" ;;
    esac
    url="https://go.dev/dl/$file"
    info "trying Go $v tarball ($file)"
    tmpd="$(mktemp -d 2>/dev/null || echo "$HOME/.nimbus-tmp-$$")"
    mkdir -p "$tmpd"
    if curl -fsSL --retry 3 --max-time 300 -o "$tmpd/$file" "$url"; then
      rm -rf "$HOME/.local/nimbus-go" && mkdir -p "$HOME/.local"
      if tar -xzf "$tmpd/$file" -C "$HOME/.local" 2>/dev/null && [ -x "$HOME/.local/go/bin/go" ]; then
        mv "$HOME/.local/go" "$HOME/.local/nimbus-go"
        export PATH="$HOME/.local/nimbus-go/bin:$PATH"
        rm -rf "$tmpd"
        info "Go $v installed to ~/.local/nimbus-go (add to PATH permanently)"
        return 0
      fi
    fi
    rm -rf "$tmpd"
    warn "tarball $v failed, trying next"
  done
  return 1
}
if [ "$SKIP_GO" = 1 ]; then
  info "--skip-go: trusting existing toolchain"
  have go || die "no go binary and --skip-go given — remove the flag or install Go 1.22+"
elif ! go_ok; then
  info "installing Go toolchain"
  if [ "$OS" = "termux" ]; then
    (pkg install -y golang 2>/dev/null && go_ok) || install_go_tarball || die "Go install failed — see https://go.dev/doc/install, then re-run with --skip-go"
  elif [ "$OS" = "darwin" ] && have brew; then
    (brew install go 2>/dev/null && go_ok) || install_go_tarball || die "Go install failed — update Xcode tools (xcode-select --install), then re-run"
  else
    (try_install golang 2>/dev/null && go_ok) || (try_install go 2>/dev/null && go_ok) || install_go_tarball || die "Go install failed and no tarball worked — install Go 1.22+ manually from https://go.dev/dl/, then re-run with --skip-go"
  fi
  go_ok || die "go still unusable after install — check PATH, then re-run with --skip-go"
else
  info "Go OK: $(go version)"
fi

# --- 5. Sources ---
if [ "$LOCAL" = 1 ]; then
  [ -f "go.mod" ] || die "--local needs the repo root (go.mod not found here)"
  info "--local: building in place"
else
  if [ -d "$DIR/.git" ]; then
    info "updating existing checkout at $DIR"
    (cd "$DIR" && git pull --ff-only 2>/dev/null) || warn "git pull failed (offline or diverged?) — building current checkout"
  elif [ -e "$DIR" ]; then
    die "$DIR exists but is not a git checkout — move it or pass --dir=PATH"
  else
    info "cloning https://github.com/$REPO.git → $DIR"
    git clone "https://github.com/$REPO.git" "$DIR" || die "clone failed — check network and repo name (override: --repo OWNER/NAME). SSH-only networks: git clone git@github.com:$REPO.git"
  fi
  cd "$DIR" || die "cannot enter $DIR"
fi

# --- 6. Build (static, no Cgo anywhere) ---
info "building (CGO_ENABLED=0)…"
export CGO_ENABLED=0
if ! go build -o nimbus-one ./cmd/nimbus-one; then
  warn "build failed — attempting dependency repair (go mod tidy)"
  go mod tidy 2>/dev/null
  go build -o nimbus-one ./cmd/nimbus-one || die "build still failing — paste the error above when asking for help (include: $(go version), $OS/$ARCH)"
fi
./nimbus-one version || die "built binary won't start — antivirus/sandbox? Try: file ./nimbus-one"

# --- 7. Init + verify ---
if [ "$NO_INIT" = 0 ]; then
  info "first-time setup (safe to re-run)"
  ./nimbus-one init --auto || warn "init reported issues — continuing, run ./nimbus-one doctor"
  info "health check"
  ./nimbus-one doctor || warn "doctor found failures — each lists its fix; details: ./nimbus-one fix <symptom>"
fi

info "done. Next:"
info "  ./nimbus-one config        # keys + YOUR fallback order (recommended: OpenRouter)"
info "  ./nimbus-one run \"hello\"  # first task"
info "  ./nimbus-one serve         # API + Telegram + heartbeat"
info "Add to PATH: export PATH=\"\$PWD:\$PATH\"  (or make install)"
if ! ask "run selftest now (15 failure scenarios, ~10s)?"; then
  info "skipped — run ./nimbus-one selftest any time"
else
  ./nimbus-one selftest | tail -3
fi
