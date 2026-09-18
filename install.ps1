# Nimbus-One installer — Windows (PowerShell 5.1+).
# Inspect first, then run from the repo root (or any dir for net install):
#   powershell -ExecutionPolicy Bypass -File install.ps1 [-Yes] [-Dir DIR] [-Repo OWNER/NAME] [-SkipGo] [-NoInit]
# Idempotent: safe to re-run; it repairs instead of duplicating.
param(
  [switch]$Yes,
  [string]$Dir = "$env:USERPROFILE\nimbus-one",
  [string]$Repo = $(if ($env:NIMBUS_REPO) { $env:NIMBUS_REPO } else { "bm-a/nimbus-one" }),
  [switch]$SkipGo,
  [switch]$NoInit,
  [switch]$Local
)

$ErrorActionPreference = "Stop"
function Say([string]$m) { Write-Host "[nimbus] $m" }
function Warn([string]$m) { Write-Warning "[nimbus] $m" }
function Fail([string]$m) { Write-Error "[nimbus] FATAL: $m"; exit 1 }
function Confirm([string]$m) {
  if ($Yes) { return $true }
  $a = Read-Host "[nimbus] $m [y/N]"
  return ($a -eq "y" -or $a -eq "yes")
}

# --- 1. Admin-free package path: winget, else manual ---
function Have($c) { $null -ne (Get-Command $c -ErrorAction SilentlyContinue) }

if (-not (Have git)) {
  Say "installing git..."
  if (Have winget) { winget install --silent Git.Git -e 2>$null }
  if (-not (Have git)) { Fail "git missing and winget couldn't provide it — install from https://git-scm.com/download/win, then re-run" }
}
if (-not $SkipGo) {
  $goOk = $false
  if (Have go) {
    try {
      $v = (go version) | Select-String -Pattern 'go(\d+)\.(\d+)' | ForEach-Object { $_.Matches[0].Groups }
      if ([int]$v[1].Value -gt 1 -or ([int]$v[1].Value -eq 1 -and [int]$v[2].Value -ge 22)) { $goOk = $true }
    } catch { $goOk = $false }
  }
  if (-not $goOk) {
    Say "installing Go toolchain..."
    if (Have winget) { winget install --silent GoLang.Go -e 2>$null }
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
    if (-not (Have go)) { Fail "Go install failed — install Go 1.22+ from https://go.dev/dl/ and re-open the terminal, then re-run" }
  }
  Say ("Go OK: " + (go version))
}

# --- 2. Sources ---
if ($Local) {
  if (-not (Test-Path "go.mod")) { Fail "--Local needs the repo root (go.mod not found here)" }
  Say "--Local: building in place"
} else {
  if ((Test-Path $Dir) -and (Test-Path (Join-Path $Dir ".git"))) {
    Say "updating existing checkout at $Dir"
    try { Push-Location $Dir; git pull --ff-only 2>$null; Pop-Location } catch { Warn "git pull failed (offline or diverged?) — building current checkout" }
  } elseif (Test-Path $Dir) {
    Fail "$Dir exists but is not a git checkout — move it or pass -Dir PATH"
  } else {
    Say "cloning https://github.com/$Repo.git"
    try { git clone "https://github.com/$Repo.git" $Dir } catch { Fail "clone failed — check network and repo name (-Repo OWNER/NAME)" }
  }
  Set-Location $Dir
}

# --- 3. Build (static, no Cgo) ---
Say "building (CGO_ENABLED=0)..."
$env:CGO_ENABLED = "0"
try { go build -o nimbus-one.exe ./cmd/nimbus-one }
catch {
  Warn "build failed — attempting dependency repair (go mod tidy)"
  go mod tidy 2>$null
  try { go build -o nimbus-one.exe ./cmd/nimbus-one } catch { Fail "build still failing — paste the error above when asking for help" }
}
try { .\nimbus-one.exe version } catch { Fail "built binary won't start — antivirus quarantine? Check Windows Security, then re-run" }

# --- 4. Init + verify ---
if (-not $NoInit) {
  Say "first-time setup (safe to re-run)"
  try { .\nimbus-one.exe init --auto } catch { Warn "init reported issues — continuing, run .\nimbus-one.exe doctor" }
  Say "health check"
  try { .\nimbus-one.exe doctor } catch { Warn "doctor found failures — each lists its fix" }
}

Say "done. Next: .\nimbus-one.exe config | .\nimbus-one.exe run 'hello' | .\nimbus-one.exe serve"
if (Confirm "run selftest now (15 failure scenarios)?") { .\nimbus-one.exe selftest | Select-Object -Last 3 }
else { Say "skipped — run .\nimbus-one.exe selftest any time" }
