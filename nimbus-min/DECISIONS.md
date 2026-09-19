# DECISIONS — safety vs convenience log

Every trade-off made during development, with the reasoning. Newest
last. Nothing here is hidden in code comments alone.

1. Shell confirmation is mandatory with no bypass.
   The brief originally allowed narrow pre-approved patterns; you
   corrected this to 100% mandatory typed-`yes`, no allowlists, no
   flags. Reason: the user is a non-programmer; any bypass is a
   future silent-execution bug. Cost: repetitive `yes` typing on
   long tasks. Accepted.

2. Headless shell = refuse, not queue.
   The app bridge and piped runs cannot prompt, so shell calls fail
   with guidance instead of running. Alternative (execute with a
   policy) was rejected: silent execution is exactly what the gate
   exists to prevent.

3. Absolute paths rejected even inside the workspace.
   Stricter than necessary for safety (an in-workspace absolute path
   is harmless), but it forces one path style, which makes transcripts
   predictable and keeps the jail audit to a single rule. Cost: the
   model must learn relative paths; the system prompt teaches it.

4. Single Go package, stdlib only.
   No web framework, no TUI library, no ORM, no sqlite driver. Reason:
   the scope (5 tools, 1 model) does not justify dependencies, and
   every dependency is network/code surface a non-programmer cannot
   audit. The WebSocket upgrade is ~40 lines of hand-rolled RFC6455
   for the same reason.

5. No persistence beyond the config file.
   Sessions, memory, and history end with the process. The brief
   forbids databases; operationally, transcripts are re-derivable
   from the workspace (git) plus the user's chat log. If this hurts,
   it hurts visibly and can be revisited explicitly.

6. Anthropic-only, model pinned.
   One client, one model constant, one prompt style. Multi-provider
   routing is a cost/choice feature for a later, explicitly approved
   version — not a silent fallback chain.

7. App compatibility is protocol-only.
   We implement the wire surface the app needs (connect, chat.send,
   sessions.list) and answer UNKNOWN_METHOD for the rest. We do not
   import OpenClaw features (pairing, nodes, cron, skills) to look
   more compatible. Compatibility claims cover only tested methods.

8. Edit requires exactly-once match.
   Zero matches and multi-matches both error with a snippet instead
   of guessing. Reason: silent wrong-file edits are the worst
   data-loss mode for a non-programmer. Cost: extra read/narrow
   turns; the loop is built to spend them.

9. Conservative token estimation is out of scope.
   No context budgeting in v1: the 25-turn cap plus the model's own
   discipline bounds cost. If long sessions degrade, that is a
   measured Phase-2 feature, not a v1 guess.
