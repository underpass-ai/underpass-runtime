# Underpass Runtime

[![CI](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg)](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml)
[![CodeQL](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/underpass-ai/underpass-runtime/security/code-scanning)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev)

A governed execution plane for AI agents that code. Give your agents
99 real-world tools (filesystem, git, build, test, deploy, security scan)
inside isolated workspaces. Every tool invocation is policy-checked,
telemetry-recorded, and artifact-preserved.

The runtime **learns which tools work best** for each context and adapts
its recommendations automatically. A background pipeline (CronJob) trains
policies from telemetry, progressing from heuristics to Neural Thompson
Sampling as data accumulates. The online path consumes these policies.

## Why this exists

Most agent frameworks give tools to LLMs without governance. The agent
calls `rm -rf /`, sends secrets to external APIs, or burns tokens
retrying tools that always fail. Underpass Runtime solves this:

- **Isolation**: each agent session gets its own workspace (local, Docker, or K8s pod)
- **Policy**: every tool call passes through an authorization engine before execution
- **Telemetry**: every outcome (success, failure, latency, cost) is recorded
- **Learning**: the system learns from outcomes and recommends better tools over time
- **Evidence**: every recommendation is auditable — you can trace why a tool was suggested

## How it works

```
Event fires (task assigned, PR opened, build broken)
    |
    v
Agent activates, creates a session
    |
    v
Runtime recommends tools (adaptive: heuristic -> Thompson -> NeuralTS)
    |
    v
Agent invokes tools in isolated workspace
    |
    v
Telemetry recorded, policies improve, next event gets better recommendations
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

# Validate everything works (15 tests against live cluster)
helm test underpass-runtime --timeout 10m
```

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

See [Algorithm Architecture](docs/ARCHITECTURE_ALGORITHMS.md) for the
full technical design.

## Tool catalog

99 capabilities across 23 families:

| Family | Tools | Examples |
|--------|------:|---------|
| `fs.*` | 10 | read, write, search, patch, stat, copy, move, delete |
| `git.*` | 11 | status, diff, commit, push, log, branch, checkout |
| `repo.*` | 14 | detect, build, test, coverage, symbols, static_analysis |
| `k8s.*` | 8 | get_pods, apply_manifest, rollout, logs, services |
| `redis.*` | 7 | get, set, del, scan, mget, exists, ttl |
| `security.*` | 4 | scan_dependencies, scan_secrets, scan_container, license_check |
| `container.*` | 4 | run, exec, logs, ps |
| + 16 more | 41 | node, go, rust, python, c, image, kafka, nats, mongo, ... |

Every tool carries metadata: risk level, side effects, cost hint, approval
requirements, idempotency. The policy engine uses this to enforce
governance rules before execution.

Full catalog: [docs/CAPABILITY_CATALOG.md](docs/CAPABILITY_CATALOG.md)

## E2E tested

15 tests run as Kubernetes Jobs via `helm test` against a live mTLS cluster:

| Category | Tests | What they prove |
|----------|-------|----------------|
| Smoke | 4 | Health, sessions, discovery, basic invocations |
| Core | 5 | Policy enforcement, recommendations, data flow, evidence, learning |
| Full | 6 | Multi-agent pipelines, event-driven agents, NeuralTS, LLM loops, full infra |

Every test validates real gRPC calls over mTLS with JetStream event verification.
See [e2e/README.md](e2e/README.md) for evidence.

## gRPC API

Proto definitions: `specs/underpass/runtime/v1/runtime.proto`

| Service | RPCs | Purpose |
|---------|------|---------|
| **SessionService** | CreateSession, CloseSession | Workspace lifecycle |
| **CapabilityCatalogService** | ListTools, DiscoverTools, RecommendTools | Tool discovery + adaptive recommendations |
| **InvocationService** | InvokeTool, GetInvocation, GetLogs, GetArtifacts | Governed tool execution |
| **LearningEvidenceService** | GetRecommendationDecision, GetEvidenceBundle | Recommendation audit trail |
| **HealthService** | Check | Health + readiness |

## Documentation

Start here:

| Doc | What you learn |
|-----|---------------|
| [Algorithm Architecture](docs/ARCHITECTURE_ALGORITHMS.md) | How recommendations work — scoring, NeuralTS, selection |
| [Configuration Reference](docs/CONFIGURATION.md) | 80+ environment variables |
| [Helm Install Guide](docs/HELM_INSTALL.md) | Deploy to Kubernetes with mTLS |
| [TLS Guide](docs/DEPLOYMENT-TLS.md) | mTLS for all 5 transports |
| [Evidence Plane](docs/EVIDENCE_PLANE.md) | Recommendation traceability |
| [CI Automation](docs/CI_AUTOMATION.md) | Image builds, registry, versioning |
| [Tool Catalog Guide](docs/TOOL_CATALOG_GUIDE.md) | How to add new tools |
| [E2E Evidence](e2e/README.md) | Test results with logs |

Architecture decisions: [docs/adr/](docs/adr/)

## Part of Underpass AI

| Repository | Role |
|-----------|------|
| **underpass-runtime** (this) | Tool execution + telemetry + adaptive learning |
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
