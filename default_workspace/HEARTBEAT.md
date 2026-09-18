# HEARTBEAT.md — Proactive checks

Nimbus-One ticks through this checklist on its heartbeat interval
(default every 15m, throttled on low battery). Unchecked items run;
checked items are skipped until reset. Reply HEARTBEAT_OK when quiet.

- [ ] System health: disk space OK, no crashed sessions
- [ ] Skills directory changed? Reload registry if so
- [ ] Anything in the workspace need attention (TODO/FIXME)?
- [ ] Memory ends the run quiet — no notification unless findings
