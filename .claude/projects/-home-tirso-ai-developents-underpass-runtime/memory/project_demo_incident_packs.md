---
name: Demo redesign — incident packs, not scripted sequences
description: User vision for event-driven demo with specialized agents reacting to observability alerts
type: project
---

User defined the architecture for dynamic demos on 2026-04-02. Core principle:
**events are the unit, not repos or prompts.**

**Incident Pack structure** (proposed):
- `observability.alert.fired` — trigger event with service, symptom, repo, commit
- `diagnostic-agent` → classifies, decides if rehydration needed → `incident.diagnosed`
- `rehydration-agent` → kernel context (runbooks, past incidents, PRs) → `context.rehydrated`
- `repair-agent` → runtime execution on real repo → `runtime.patch_applied`
- `verification-agent` → tests/smoke/validate → `verification.passed|failed`
- `strategy-agent` → replans if verification fails
- `comms-agent` → summarizes with evidence

**incident-pack.yaml** schema (to design):
- initial events (alert payload)
- subscribed agents
- repo source + commit/base
- preflight checks
- verify/oracle criteria
- TUI surfaces per phase

**Positioning**: "We don't show a coding agent. We show a system that reacts to a real alert, activates specialists, recovers surgical memory, and executes a governed repair."

**How to apply**: Next demo redesign session should start from this architecture, not patch the current screenplay.
