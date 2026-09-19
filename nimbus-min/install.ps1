# Nimbus-One installer — Windows (PowerShell 5.1+).
# Read this file first, then run from anywhere:
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-Yes] [-Dir DIR] [-Repo OWNER/NAME] [-NoOnboard] [-Local]
# Idempotent: safe to re-run; it rebuilds instead of duplicating.
param(
  [switch]$Yes,
  [string]$Dir = "$env:USERPROFILE\nimbus-one",
  [string]$Repo = $(if ($env:NIMBUS_REPO) { $env:NIMBUS_REPO } else { "bm-a/nimbus-one" }),
  [switch]$NoOnboard,
  [switch]$Local
)

$ErrorActionPreference = "Stop"
function Say([string]$m) { Write-Host "[nimbus] $m" }
function Fail([string]$m) { Write-Error "[nimbus] FATAL: $m"; exit 1 }
function Confirm([string]$m) {
  if ($Yes) { return $true }
  $a = Read-Host "[nimbus] $m [y/N]"
  return ($a -eq "y" -or $a -eq "yes")
}
function Have($c) { $null -ne (Get-Command $c -ErrorAction SilentlyContinue) }

# --- 1. git + go ---
if (-not (Have git)) { Fail "git not found — install from https://git-scm.com/download/win, then re-run" }
if (-not (Have go)) { Fail "go not found — install from https://go.dev/dl/ (1.24+), then re-run" }

# --- 2. sources ---
if ($Local) {
  if (-not (Test-Path "nimbus-min/go.mod")) { Fail "--Local needs the repo root (nimbus-min/go.mod not here)" }
  $Src = (Get-Location).Path
  Say "using local checkout: $Src"
} else {
  if (Test-Path "$Dir/nimbus-min/go.mod") {
    Say "existing checkout at $Dir — updating"
    try { git -C $Dir pull --ff-only } catch { Say "pull failed, building as-is" }
  } else {
    if (-not (Confirm "clone https://github.com/$Repo into $Dir?")) { Fail "cancelled — nothing changed" }
    git clone "https://github.com/$Repo" $Dir
    if ($LASTEXITCODE -ne 0) { Fail "clone failed" }
  }
  $Src = $Dir
}

# --- 3. build (stdlib only) ---
Say "building nimbus-min (pure Go, no dependencies)..."
$env:CGO_ENABLED = "0"
go build -trimpath -o "$Src/nimbus-min/nimbus-min.exe" ./nimbus-min
if ($LASTEXITCODE -ne 0) { Fail "build failed" }
$Bin = "$Src/nimbus-min/nimbus-min.exe"
Say "built: $Bin"
& $Bin version

# --- 4. onboard ---
if (-not $NoOnboard) {
  Say "starting first-run setup..."
  & $Bin onboard
}

Say "done. Next:"
Say "  $Bin run `"list my files`""
Say "  $Bin serve   # control page at http://127.0.0.1:8787"
