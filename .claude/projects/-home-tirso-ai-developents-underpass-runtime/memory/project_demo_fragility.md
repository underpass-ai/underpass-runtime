---
name: Demo fragility — scripted screenplay vs resilient tests
description: The demo is a hardcoded sequence that breaks on any infra/content change. Needs redesign.
type: project
---

The `underpass-demo` guided tour (`make demo-tour`) is a **scripted screenplay**: each step assumes exact files, exact diffs, exact paths, exact toolchains. Any change in the stack breaks the chain.

**Incidents hit in 2026-04-02 session (all resolved):**
1. `RehydrateSession` failed — `snapshot_ttl=nil` when `persist_snapshot=true` (demo contract bug)
2. `fs.patch` failed — `git` missing in distroless runtime image (fixed: K8s backend + runner pods)
3. `fs.patch` failed — diff format wrong (demo content bug, fixed by user)
4. `repo.test` failed — `go: not found` in `base` runner (fixed: switched to `toolchains` runner)
5. `repo.test` failed — test reads `services/payments/config.yaml` with wrong relative path (demo content bug)

**Root cause:** The demo is NOT event-driven or adaptive. It's a rigid sequence where step N assumes the exact output of step N-1.

**Why it matters:** Every infra improvement (K8s backend, runner images, Helm changes) cascades into demo breakage. The demo should be the LAST thing to break, not the FIRST.

**Future direction:** Redesign the demo as an event-driven sequence with precondition checks per step — same pattern as the E2E tests (which are resilient). Or make it a thin wrapper over the actual E2E test infrastructure.

**Runner image matrix (resolved):**
- `runner:base` — git, bash, curl, jq, patch (fs.*, git.*)
- `runner:toolchains` — + Go, Rust, Node, Python (repo.*, go.*, rust.*, node.*, python.*)
- Runner pods need `imagePullSecrets` support (DEUDA TÉCNICA — currently uses `PullIfNotPresent` workaround)
