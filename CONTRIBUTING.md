# Contributing to Nimbus One

## Ground rules (coding ethics)

1. **Original work.** Ideas may be referenced (see `ATTRIBUTION.md`); code
   is written fresh. Never paste code you don't have the rights to
   contribute. New files carry no license header — the repo `LICENSE` (MIT)
   covers everything — but third-party snippets are never committed.
2. **Stdlib first.** Pure-Go modules already in `go.mod` are allowed.
   No Cgo, no runtimes, no network daemons, no vendored binaries.
3. **No stubs.** No TODOs, no placeholders. Degrade with warnings, never
   silent no-ops.
4. **Users choose.** No hardcoded model chains or silent provider changes.
   Suggestions are labeled and inert until confirmed.
5. **Secrets stay out.** Tests use fake keys. Never commit `vault.key`,
   `secrets.enc`, `config.yaml`, tokens, or transcripts with real data.
   `git` is pre-configured via `.gitignore`; check `git status` before
   every commit.

## Workflow

- Read `AGENTS.md` first — it is the contract (layout, definition of done).
- One concern per commit, message in imperative mood (`Add voice STT`).
- Verify per change: `go vet` touched packages, `go test -count=1`
  touched packages, then `go build ./...`. Fix failures — never weaken
  tests to make them pass.
- User-facing changes also update: `docs/COMMANDS.md`, the relevant
  `wiki/` page, and `CHANGELOG.md` (Unreleased section).
- New failures modes go in `internal/doctor/knowledge.go` AND
  `docs/TROUBLESHOOTING.md` (keep them in sync) with a `selftest`
  scenario when the failure is simulatable.

## Reporting bugs / requesting features

Run `nimbus-one doctor --bundle support.zip` and attach it. For
regressions, also paste the failing `nimbus-one selftest` scenario id.
Security issues: see `SECURITY.md` — do not file them publicly.
