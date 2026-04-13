---
name: service.go refactor candidate
description: internal/app/service.go exceeds 1200 lines — user flagged for refactoring
type: project
---

`internal/app/service.go` is >1200 lines. User explicitly flagged it for refactoring on 2026-04-02.

**Why:** file is growing with each feature (invocation, telemetry, events, evidence). Hard to navigate.

**How to apply:** In a future session, consider extracting:
- Evidence methods (GetRecommendationDecision, GetEvidenceBundle) into a dedicated file
- Telemetry recording into its own file
- Event publishing helpers into their own file
- Invocation lifecycle (invoke, get, artifacts, logs) into a dedicated file
Keep the Service struct and constructor in service.go; move method implementations out.
