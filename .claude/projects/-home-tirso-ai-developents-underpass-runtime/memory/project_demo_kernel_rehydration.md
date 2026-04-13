---
name: demo-kernel-rehydration
description: Product demo plan — tool-learning + kernel rehydration joint demo for WOW effect
type: project
---

The user wants to prepare a product demo combining tool-learning and kernel rehydration.

**Why:** The demo must show the full closed-loop cycle — telemetry → Thompson Sampling → policies → agent context rehydration → tool ranking at invocation time. This is the "super WOW" moment.

**How to apply:** When working on demo-related tasks, design for the full cycle. tool-learning writes to Valkey, kernel rehydration reads from Valkey to build agent context. The demo should show live propagation of ranking changes.

Demo created: `services/tool-learning/cmd/demo/` — zero-infra self-contained demo (DuckDB + miniredis + NATS in-memory). Run with `make demo`.
