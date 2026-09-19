# Agent Guide

For AI coding agents contributing to `nimbus-min/` in this repo.
Human docs: [User Guide](User-Guide.md) · [FAQ](FAQ.md).

## Module layout (one package, stdlib only)

```
nimbus-min/
  main.go          CLI: onboard | run | serve | version | help
  onboard.go       first-run wizard (consent → key → workspace → config)
  config.go        nimbus.json load/save/validate (0600, unknown fields rejected)
  loop.go          agent loop + 5 tool definitions + system prompt
  anthropic.go     /v1/messages client (pinned version header)
  netguard.go      ONLY dial path: api.anthropic.com:443
  tools.go         read/write/edit/list/shell (all through the jail)
  jail.go          workspace lock, resolve(), shell-text scanner
  confirm.go       mandatory typed-yes gate, headless refusal
  compat.go        app bridge: WS frames + green control page
  install.sh       tested Unix installer (see below)
  install.ps1      Windows installer (NOT runnable here — review only)
```

Rules: no new dependencies (stdlib only), no new tools without
explicit approval, no `TODO`/stubs, `gofmt` clean, `go vet` clean.

## Jail rules (do not weaken)

- `resolve()`: relative-only, no absolute (even in-workspace), no
  `..`, no NUL; symlinks resolved incl. longest-existing-ancestor
  for not-yet-existing paths; containment re-checked after resolution.
- `checkShellText()`: absolute tokens, `..` segments, `cd /…`
  rejected; scanner splits on quotes/operators so quoting hides nothing.
- `cmd.Dir` locked to workspace root — defense in depth behind the checks.
- Any change here needs new adversarial tests in `jail_test.go` /
  `tools_test.go` proving the attack still fails.

## Shell gate (do not bypass)

`confirmShell` is the single chokepoint: exact command +
plain-English explanation + exactly `yes`. No allowlists, no flags,
no pre-approval — a maintainer correction, permanent. Headless
refuses. Tests in `confirm_test.go`.

## Test + verify

```sh
cd nimbus-min
gofmt -l .
go vet ./...
go test -count=1 -timeout 60s ./...
CGO_ENABLED=0 go build -o nimbus-min .
```

Installer test (real GitHub clone into scratch):

```sh
sh nimbus-min/install.sh --yes --no-onboard --dir /tmp/install-test
```

CI runs the same (`min` job in `.github/workflows/ci.yml`).
`install.ps1` cannot run on Linux CI — any change to it needs
extra-careful review, and say so in the PR/commit message.

## Compat scope

Implemented + tested: `connect` (protocol 4, token auth),
`chat.send`, `sessions.list`, `UNKNOWN_METHOD` otherwise.
Do NOT add pairing/Tailscale/nodes/admin methods to "look
compatible" — untestable claims stay out. Green page is one
embedded constant (`appHTML`); keep it dependency-free.
