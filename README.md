# Underpass Runtime

[![CI](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg)](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml)
[![CodeQL](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/underpass-ai/underpass-runtime/security/code-scanning)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev)

A governed execution plane for AI agents that code. \*\*123 real-world tools\*\*
(filesystem, git, shell, build, test, deploy, security scan, web access)
inside isolated workspaces — every invocation policy-checked, telemetry-recorded,
and artifact-preserved.

<p align="center">
  <img src="docs/assets/demo.gif" alt="Underpass Runtime Demo" width="800">
</p>

The runtime **learns which tools work best** for each context and adapts
its recommendations automatically, progressing from heuristics to Neural Thompson
Sampling as data accumulates.

## What makes this different

Other agent frameworks hand tools to LLMs without governance. The agent calls
`rm -rf /`, sends secrets to external APIs, or burns tokens retrying tools that
always fail.

Underpass Runtime is the only open-source agent runtime that combines:

| Feature | Underpass Runtime | Claude Code | SWE-agent | Cursor |
|---------|:-:|:-:|:-:|:-:|
| Policy-governed tools | **114** | 24 | ~10 | ~15 |
| `tool.suggest` — agents ask "what tool for this task?" | **yes** | no | no | no |
| `policy.check` — dry-run "would this be allowed?" | **yes** | no | no | no |
| Adaptive scoring (Heuristic → Thompson → NeuralTS) | **yes** | no | no | no |
| Cross-agent learning | **yes** | no | no | no |
| Isolated K8s workspaces | **yes** | no | no | no |
| Auditable evidence trail for every recommendation | **yes** | no | no | no |

## How it works

```
Event fires (issue assigned, PR opened, build broken)
    |
    v
Agent activates, creates a session (workspace prewarmed in background)
    |
    v
Runtime recommends tools (adaptive: heuristic -> Thompson -> NeuralTS)
  + cross-agent insight: "backed by 47 invocations across 5 tools"
  + score breakdown: heuristic 1.0 + telemetry +0.15 + policy -0.06
    |
    v
Agent invokes tools in isolated workspace
    |
    v
Agent reports feedback: AcceptRecommendation / RejectRecommendation
    |
    v
Telemetry + feedback recorded -> policies improve -> next event gets better
```

Transport: gRPC over mTLS. Five infrastructure backends: Valkey (state + policies),
NATS JetStream (events), S3/MinIO (artifacts), OTLP (traces), Prometheus (metrics).

## Quick start

```bash
# Run locally — no infrastructure needed (memory backends)
go run ./cmd/workspace

# Health check
grpcurl -plaintext localhost:50053 underpass.runtime.v1.HealthService/Check

# Or deploy to Kubernetes with full mTLS
helm install underpass-runtime charts/underpass-runtime \
  --set certGen.enabled=true \
  --set stores.backend=valkey \
  --set eventBus.type=nats \
  -f charts/underpass-runtime/values.shared-infra.yaml \
  -f charts/underpass-runtime/values.mtls.example.yaml

# Validate everything works (16 tests against live cluster)
helm test underpass-runtime --timeout 10m
```

## Tool catalog — 114 capabilities

Every tool an SWE agent needs, governed by policy:

### Code navigation (the agent orients itself)

| Tool | What it does |
|------|-------------|
| `repo.tree` | Directory structure in one call — instant codebase orientation |
| `repo.symbols` | Extract functions/types/imports WITHOUT reading the full file — saves 90% context |
| `fs.glob` | Find files by pattern (`**/*.go`, `src/**/test_*.py`) |
| `fs.read_lines` | Read specific line range with line numbers — no need to load 5000-line files |
| `fs.search` | Search file contents with regex |
| `git.blame` | Who changed what line and when |
| `git.diff_file` | Diff a single file against any ref |

### Code editing (the agent makes changes)

| Tool | What it does |
|------|-------------|
| `fs.edit` | Surgical search-and-replace (fails if ambiguous — forces precision) |
| `fs.insert` | Insert text at a specific line number |
| `fs.write_file` | Create or overwrite files |
| `fs.patch` | Apply unified diffs |
| `workspace.undo_edit` | Revert the last edit — safety net for small models |

### Execution and verification

| Tool | What it does |
|------|-------------|
| `shell.exec` | Governed shell execution (`make`, `pip`, `cargo`, `curl`, anything) |
| `repo.test_file` | Run tests for ONE file — faster than the full suite |
| `repo.build` / `repo.test` | Build and test with language detection |

### Intelligence (unique to Underpass Runtime)

| Tool | What it does |
|------|-------------|
| `tool.suggest` | "I need to edit a Go function" → ranked tool recommendations with scores |
| `policy.check` | "Would `shell.exec rm -rf /` be allowed?" → denied, without executing |

### Workflow (issue → code → PR)

| Tool | What it does |
|------|-------------|
| `github.get_issue` | Read issue details, body, comments |
| `github.list_issues` | List issues with state/label filters |
| `github.create_pr` | Create pull request |
| `github.review_comments` | Read PR review feedback |
| `github.merge_pr` | Merge with squash/rebase/merge |
| `web.fetch` | Read documentation, APIs, changelogs from URLs |
| `web.search` | Search the web for error messages and solutions |

### + 80 more tools

| Family | Count | Examples |
|--------|------:|---------|
| `git.*` | 12 | status, diff, diff_file, commit, push, log, branch, blame, checkout |
| `repo.*` | 16 | detect, build, test, test_file, coverage, symbols, tree, static_analysis |
| `k8s.*` | 9 | get_pods, apply_manifest, set_image, rollout, restart, logs |
| `redis.*` | 7 | get, set, del, scan, mget, exists, ttl |
| `container.*` | 4 | run, exec, logs, ps |
| `security.*` | 5 | scan_dependencies, scan_secrets, scan_container, license_check, sbom |
| Language toolchains | 14 | go.build, rust.test, node.lint, python.validate, c.build |
| Messaging | 9 | nats.publish, kafka.produce, rabbit.consume |

Every tool carries metadata: risk level, side effects, cost hint, approval
requirements, idempotency. The policy engine uses this to enforce
governance rules before execution.

Full catalog: [docs/capability-catalog.md](docs/capability-catalog.md)

## Adaptive recommendation engine

The runtime scores tools using a 4-tier stack. The online path selects
the best available scorer; the offline pipeline (tool-learning CronJob)
trains policies and neural models that the online path consumes:

| Data maturity | Algorithm | Online / Offline |
|--------------|-----------|-----------------|
| No data | **Heuristic** | Online: scores by risk, cost, task hint matching |
| 5+ invocations | **+ Telemetry boost** | Online: adjusts for success rate, latency, deny rate |
| 50+ samples | **Thompson Sampling** | Offline: Beta policies in Valkey. Online: samples Beta posterior |
| 100+ samples + model | **Neural Thompson Sampling** | Offline: trains MLP, publishes weights. Online: forward pass + perturbation |

Selection is automatic. The active algorithm is visible in every response:

```json
{
  "recommendation_id": "rec-ce557598c511be67",
  "algorithm_id": "neural_thompson_sampling",
  "decision_source": "neural_thompson_sampling",
  "policy_mode": "shadow"
}
```

See [Algorithm Architecture](docs/architecture/algorithms.md) for the
full technical design.

## E2E tested on live mTLS cluster

16 tests run as Kubernetes Jobs against a live mTLS cluster:

| Category | Tests | What they prove |
|----------|-------|----------------|
| Smoke | 4 | Health, sessions, discovery, basic invocations |
| Core | 6 | Policy enforcement, recommendations, data flow, evidence, learning, **SWE agent tools** |
| Full | 6 | Multi-agent pipelines, event-driven agents, NeuralTS, LLM loops, full infra |

Test 22 validates the full SWE agent workflow:
`shell.exec` → `repo.tree` → `fs.glob` → `fs.read_lines` → `fs.edit` → `fs.insert` → `git.diff_file` → `tool.suggest` → `policy.check`

Every test validates real gRPC calls over mTLS with JetStream event verification.
See [e2e/README.md](e2e/README.md) for evidence.

## gRPC API

Proto definitions: `specs/underpass/runtime/v1/runtime.proto` + `specs/underpass/runtime/learning/v1/learning.proto`

| Service | RPCs | Purpose |
|---------|------|---------|
| **SessionService** | CreateSession, CloseSession | Workspace lifecycle |
| **CapabilityCatalogService** | ListTools, DiscoverTools, RecommendTools, AcceptRecommendation, RejectRecommendation | Tool discovery, adaptive recommendations, agent feedback loop |
| **InvocationService** | InvokeTool, GetInvocation, GetLogs, GetArtifacts | Governed tool execution |
| **LearningEvidenceService** | GetLearningStatus, GetRecommendationDecision, GetEvidenceBundle, GetPolicy, ListPolicies, GetAggregate + 8 more | Recommendation audit, policy inspection, learning pipeline status |
| **HealthService** | Check | Health + readiness |

## Agent feedback loop

Agents report whether a recommendation actually solved their task:

```
Agent → RecommendTools → rec-123 (fs.read_file)
Agent → InvokeTool(fs.read_file) → succeeded
Agent → AcceptRecommendation(rec-123, fs.read_file)  // or RejectRecommendation
                    ↓
Learning pipeline adjusts future policies (reward shaping)
```

This closes the outer loop: the system learns not just "did the tool work?" but "did it solve the agent's problem?".

## Explainability trace

Every recommendation carries a machine-readable score breakdown:

```json
"score_breakdown": [
  {"name": "heuristic",       "value": 1.0,  "rationale": "low risk, no side effects"},
  {"name": "telemetry_boost", "value": 0.15, "rationale": "95% success rate (20 invocations)"},
  {"name": "beta_thompson",   "value": -0.06, "rationale": "thompson(sample=0.87, weight=0.75)"}
]
```

Persisted in the decision store and accessible via `GetRecommendationDecision`.

## Cross-agent learning

Agents in the same context automatically share learning. Every recommendation includes an insight:

```json
"insight": {
  "total_invocations": 47,
  "tool_count": 5,
  "confidence": "medium",
  "algorithm_tier": "thompson"
}
```

Confidence levels: **low** (<20 invocations), **medium** (20-99), **high** (100+ with active policy).

## Autonomous remediation

The `remediation-agent` subscribes to NATS alert events and auto-runs playbooks:

| Alert | Action |
|-------|--------|
| WorkspaceInvocationFailureRateHigh | Diagnose error patterns |
| WorkspaceP95InvocationLatencyHigh | Investigate latency spikes |
| WorkspaceInvocationDeniedRateHigh | Audit policy denials |
| WorkspaceDown | Emergency health check |

Flow: Grafana alert → alert-relay → NATS → remediation-agent → session + tools + feedback → close.

## Observability

- **Prometheus metrics** at `:9090` (configurable via `METRICS_PORT`): invocations, latency histograms, denial rates, 11 KPI metrics
- **OpenTelemetry traces** with `trace_id` + `span_id` injected into every structured log (TraceLogHandler)
- **OTEL Collector** managed by the Helm chart (`otelCollector.enabled`)
- **Grafana dashboard** auto-provisioned via `charts/observability-stack/` (8 panels)
- **Loki** for structured log aggregation via Promtail
- Separate observability stack: [underpass-ai/underpass-observability](https://github.com/underpass-ai/underpass-observability)

## What we're working on next

- [ ] LSP integration — type errors without full builds
- [ ] Workspace checkpoints — snapshot/rollback for multi-step refactors
- [ ] Agent spawn — sub-agent delegation as a first-class tool
- [ ] Semantic code search — find code by meaning, not just regex

## Documentation

Start here:

| Doc | What you learn |
|-----|---------------|
| [Algorithm Architecture](docs/architecture/algorithms.md) | How recommendations work — scoring, NeuralTS, selection |
| [Configuration Reference](docs/configuration.md) | 80+ environment variables |
| [Helm Install Guide](docs/helm-install.md) | Deploy to Kubernetes with mTLS |
| [TLS Guide](docs/operations/deployment-tls.md) | mTLS for all 5 transports |
| [Evidence Plane](docs/architecture/evidence-plane.md) | Recommendation traceability |
| [CI Automation](docs/operations/ci-automation.md) | Image builds, registry, versioning |
| [Tool Catalog Guide](docs/tool-catalog-guide.md) | How to add new tools |
| [E2E Evidence](e2e/README.md) | Test results with logs |

Architecture decisions: [docs/adr/](docs/adr/)

## Part of Underpass AI

| Repository | Role |
|-----------|------|
| **underpass-runtime** (this) | Tool execution + telemetry + adaptive learning |
| [underpass-observability](https://github.com/underpass-ai/underpass-observability) | Grafana, Loki, OTEL Collector, Prometheus, alert relay |
| [rehydration-kernel](https://github.com/underpass-ai/rehydration-kernel) | Surgical context from knowledge graphs |
| [swe-ai-fleet](https://github.com/underpass-ai/swe-ai-fleet) | Multi-agent SWE platform |
| [underpass-demo](https://github.com/underpass-ai/underpass-demo) | See it working together |

## Legal

Copyright © 2026 Tirso García Ibáñez.

This repository is part of the Underpass AI project.
Licensed under the Apache License, Version 2.0, unless stated otherwise.

Redistributions and derivative works must preserve applicable copyright,
license, and NOTICE information.

Original author: [Tirso García Ibáñez](https://github.com/tgarciai) · [LinkedIn](https://www.linkedin.com/in/tirsogarcia/) · [Underpass AI](https://github.com/underpass-ai)
