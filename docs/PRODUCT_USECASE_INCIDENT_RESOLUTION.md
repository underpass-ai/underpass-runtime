# Production Incident Resolution — Runtime Perspective

## What the Runtime Provides

The Underpass Runtime is a **governed execution plane** for AI agents.
For the production incident resolution use case, it provides:

- **99 tools across 23 families**: fs.*, git.*, repo.*, k8s.*, security.*,
  container.*, and more
- **Isolated workspaces**: local, Docker, or Kubernetes-backed sessions
- **Policy enforcement**: every tool invocation is authorized before execution
- **4-tier adaptive learning**: heuristic → Thompson Sampling → Neural
  Thompson Sampling — the system learns which tools work best
- **Telemetry**: success/failure, latency, cost recorded for all invocations
- **Evidence plane**: every recommendation is auditable with score breakdowns

---

## How the Runtime Serves This Use Case

### Full incident resolution flow

```
1. INVESTIGATE (triage + diagnostic agents)
   ├── fs.read_file    — read service config, source code
   ├── git.log         — check recent changes
   ├── git.diff        — identify config regressions
   └── repo.static_analysis — analyze code structure

2. FIX (repair agent)
   ├── fs.write_file   — write corrected config
   ├── fs.patch        — apply unified diff to source
   ├── repo.test       — run test suite, verify fix
   └── repo.validate   — validate config consistency

3. DEPLOY (repair + deploy agents)
   ├── git.branch      — create hotfix/payments-pool-recovery
   ├── git.commit      — commit the fix with incident reference
   ├── git.push        — push branch to remote
   ├── github.create_pr — open PR with root cause description
   ├── github.check_pr_status — wait for CI checks
   └── github.merge_pr — merge when green

4. VERIFY (verification agent)
   ├── prometheus.query — check saturation dropped below threshold
   └── k8s.get_pods    — confirm new pod is running
```

### Tool selection is learned, not hardcoded

The fixture defines which tools to use for the demo sequence. But in
production, the **recommendation engine** selects tools based on:

1. **Heuristic** (always): base score from tool metadata (risk, cost,
   side effects, idempotency)
2. **Telemetry** (n ≥ 5): success rate, latency, deny rate adjustments
3. **Thompson Sampling** (n ≥ 50): Bayesian exploration/exploitation
4. **Neural Thompson Sampling** (n ≥ 100): MLP-based policy with context

After 100+ incidents, the runtime knows: "for payments-api pool issues,
`fs.patch` on config + `repo.test` has 95% success rate. Skip
`repo.static_analysis` — it adds 3s latency with zero diagnostic value
for config-only fixes."

---

## Tool Inventory for Incident Resolution

### Already implemented (ready to use)

| Family | Tools | Used for |
|--------|-------|----------|
| `fs.*` | read, write, search, patch, stat, list | Code analysis, config patching |
| `git.*` | status, diff, commit, push, log, branch, checkout, pull, fetch, apply_patch, show | Version control operations |
| `repo.*` | detect, build, test, coverage, symbols, static_analysis | Build verification |
| `k8s.*` | get_pods, apply_manifest, rollout, logs, services, get_deployments, get_images | Kubernetes operations |
| `security.*` | scan_dependencies, scan_secrets, scan_container, license_check | Security verification |

### Needs implementation

| Tool | Issue | Complexity | Description |
|------|-------|------------|-------------|
| `github.create_pr` | [#107](https://github.com/underpass-ai/underpass-runtime/issues/107) | Medium | Create PR via GitHub API |
| `github.check_pr_status` | [#107](https://github.com/underpass-ai/underpass-runtime/issues/107) | Low | Poll CI check status |
| `github.merge_pr` | [#107](https://github.com/underpass-ai/underpass-runtime/issues/107) | Medium | Merge PR when checks pass |
| `prometheus.query` | [#108](https://github.com/underpass-ai/underpass-runtime/issues/108) | Low | Query Prometheus for metrics |

Implementation approach: add a `GitHubBundle` to the registry pattern
(`internal/bootstrap/registry.go`). The handlers use the `gh` CLI or
GitHub REST API. Authentication via GitHub token in session credentials.

---

## Agent Purpose in Tool Invocation

Each agent has a defined purpose that constrains which tools it can use
and how. This purpose should be passed as metadata in `InvokeTool`.

### Current state

`InvokeTool` accepts session_id, tool_name, correlation_id, args, approved.
Policy evaluation checks session-level permissions.

### Target state ([#109](https://github.com/underpass-ai/underpass-runtime/issues/109))

`InvokeTool` also accepts `agent_purpose` metadata:

```protobuf
message InvokeToolRequest {
  string session_id     = 1;
  string tool_name      = 2;
  string correlation_id = 3;
  google.protobuf.Struct args = 4;
  bool approved         = 5;
  AgentPurpose purpose  = 6;  // NEW
}

message AgentPurpose {
  string agent_id          = 1;
  string role              = 2;
  repeated string autonomy_boundary = 3;
  repeated string allowed_capabilities = 4;
}
```

Benefits:
- **Policy enforcement**: repair-agent cannot invoke `github.merge_pr`
  (outside its autonomy boundary). deploy-agent can.
- **Telemetry enrichment**: tool success rates segmented by agent role.
  "fs.patch succeeds 95% when called by repair-agent, 60% by triage-agent"
- **Learning signal**: the recommender knows what the agent is trying to do
  and can suggest better tools for that specific purpose.

---

## Integration Model

### How the runtime connects to client infrastructure

The runtime operates in **Underpass's namespace**. It connects to client
systems through session configuration:

```
Runtime session
  ├── repo_url: https://github.com/client/payments-api.git  (client's repo)
  ├── credentials: GitHub token for push/PR operations
  ├── prometheus_url: http://prometheus.client-obs.svc:9090  (client's Prometheus)
  ├── kubernetes_context: client namespace for k8s.* tools
  └── allowed_paths: [services/rust/payments-api/**]  (scoped access)
```

The runtime **never modifies client systems directly**. All changes go
through the governed workflow:
1. Changes are made in an isolated workspace (cloned repo)
2. Changes are pushed via git (auditable commit history)
3. Changes are deployed via the client's CI/CD (their pipeline, their controls)
4. The runtime monitors but doesn't execute the deployment

### What the client provides

- Git repository access (read + push to branches)
- GitHub API access (create/merge PRs)
- Prometheus access (read-only for metric verification)
- CI/CD pipeline (triggered by merge to main)

### What the runtime provides

- Isolated workspace for each agent session
- Policy enforcement on all tool invocations
- Audit trail for every action
- Learning-based tool recommendations
- Evidence collection (logs, artifacts, outputs)

---

## Task Planning

### Already implemented

- [x] 99 tools across 23 families with full catalog metadata
- [x] 11 git tools (status, diff, commit, push, branch, etc.)
- [x] 8 Kubernetes tools
- [x] Session lifecycle with workspace isolation
- [x] Policy engine with RBAC
- [x] 4-tier adaptive recommendation (heuristic → NeuralTS)
- [x] Telemetry recording for all invocations
- [x] Evidence plane with recommendation traceability
- [x] 15 E2E tests via helm test

### Phase B: New tools (Priority 1)

| Issue | Task | Complexity |
|-------|------|------------|
| [#107](https://github.com/underpass-ai/underpass-runtime/issues/107) | GitHub tools (create_pr, check_pr_status, merge_pr) | Medium |
| [#108](https://github.com/underpass-ai/underpass-runtime/issues/108) | prometheus.query tool | Low |

**Implementation plan for #107:**
1. Add `GitHubBundle` to `internal/bootstrap/registry.go`
2. Create handlers in `internal/adapters/tools/`:
   - `github_create_pr.go` — POST to GitHub API or `gh pr create`
   - `github_check_pr.go` — GET checks status or `gh pr checks`
   - `github_merge_pr.go` — PUT merge or `gh pr merge`
3. Add 3 entries to `catalog_defaults.yaml` with metadata:
   - scope: external, side_effects: irreversible (merge), risk: high
4. Unit tests with mock HTTP server
5. E2E test: branch → commit → push → PR → check → merge

**Implementation plan for #108:**
1. Add `prometheus_query.go` handler
2. HTTP GET to `/api/v1/query` with PromQL expression
3. Return: value, timestamp, threshold_met (boolean)
4. Add to `catalog_defaults.yaml`: scope external, read-only, low risk

### Phase D: Agent purpose (Priority 2)

| Issue | Task | Complexity |
|-------|------|------------|
| [#109](https://github.com/underpass-ai/underpass-runtime/issues/109) | Agent purpose in InvokeTool metadata | Medium |

1. Add `AgentPurpose` message to proto
2. Extend policy evaluation to check autonomy boundaries
3. Include purpose in telemetry records
4. Use purpose in recommendation context

---

## Cross-Agent Learning for Incident Resolution

The runtime's learning loop is particularly valuable for incident resolution:

### Tool success rates by context

After multiple incidents, the runtime knows:
- `fs.patch` on YAML config: 98% success (simple, deterministic)
- `fs.patch` on Go source: 72% success (merge conflicts, syntax errors)
- `repo.test` after config patch: 95% success
- `repo.test` after source patch: 68% success

### Recommendation improvement

First incident (heuristic only):
```
Recommended: fs.patch (score: 0.7), repo.build (0.6), repo.test (0.5)
```

Tenth incident (Thompson Sampling active):
```
Recommended: fs.patch (score: 0.92), repo.test (0.88), git.commit (0.85)
Skipped: repo.build (0.3 — never needed for config-only fixes)
```

### Feedback loop

```
Agent invokes tool → success/failure recorded
  → Agent accepts/rejects recommendation
    → Learning pipeline adjusts policies
      → Next incident: better tool selection
```

This is how the runtime gets **smarter over time** — not by training new
models, but by observing which tools work in which contexts.
