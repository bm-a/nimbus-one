# Attribution

Nimbus One's code is original work. The following projects informed its design —
ideas referenced, nothing copied:

- **OpenClaw** — gateway architecture (multi-channel message routing),
  persistent Markdown state (`SOUL.md`-style identity files), proactive
  heartbeat loops, and the `SKILL.md`-with-frontmatter convention. Nimbus One
  reimplements these ideas in pure Go; no OpenClaw code is included.
- **Hermes / HermesAgent** — the ReAct observe-orient-decide-act loop,
  model fallback tiers, self-reflection prompts, planning modes, and
  context-window compaction strategy. Nimbus One's engine, guardrails, and
  compactor are independent implementations of these concepts.
- **OpenCode** — headless sidecar delegation pattern. Nimbus One shells out
  to an installed `opencode` binary (`opencode run --format json …`,
  verified against OpenCode 1.18.x where `-p` means `--password`, not
  prompt). The two tools compose: Nimbus One handles the agent loop,
  OpenCode handles giant multi-file refactors on demand.
- **Meta Muse Spark 1.3** (`meta/muse-spark-1.3-contributor`) — the
  recommended model route behind the Nimbus One agent identity, served
  OpenAI-compatible via OpenRouter's free tier or a direct Meta endpoint.
  A recommendation only; users choose their own models.
- **Charmbracelet** (`bubbletea`, `lipgloss`, `bubbles`, all pure Go) —
  the terminal UI framework behind `nimbus-one config`, the health dashboard,
  and the escalation modal.
- **fsnotify** (pure Go) — event-driven skill hot-reload, with a polling
  fallback for kernels without inotify.

Direct Go module dependencies are pinned in `go.mod`/`go.sum` (all pure
Go, no Cgo): `charmbracelet/bubbletea v1.3.10`, `charmbracelet/lipgloss
v1.1.0`, `charmbracelet/bubbles v1.0.0`, `fsnotify/fsnotify v1.10.1`,
plus their transitive deps (`muesli/*`, `mattn/*`, `rivo/uniseg`,
`lucasb-eyer/go-colorful`, `golang.org/x/sys`, `golang.org/x/text`,
`golang.org/x/exp`, and others — see `go.sum`). Each retains its own
license; everything else in this repository is MIT (see `LICENSE`).
