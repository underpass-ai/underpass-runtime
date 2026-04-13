---
name: tool-learning repo split candidate
description: User flagged services/tool-learning as candidate for separate repo — independent evolution
type: project
---

User noted on 2026-04-02 that tool-learning should evolve independently from the runtime.

**Why:** Both are separate bounded contexts with independent release cycles. Same repo complicates independent versioning and deployment.

**How to apply:** Consider splitting `services/tool-learning/` into its own repo. It already has: own go.mod, own Dockerfile, own Helm CronJob, own tests. Shared only via proto specs and NATS event contract. Future sessions may tackle this.
