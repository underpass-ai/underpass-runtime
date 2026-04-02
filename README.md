# Underpass Runtime

[![CI](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg)](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml)
[![CodeQL](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/underpass-ai/underpass-runtime/security/code-scanning)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev)

**Governed execution plane for event-driven AI agents.**

We don't build models. We build the infrastructure that makes them actually work. A 7B model with 394 tokens of surgical context outperforms a frontier model drowning in 6,000 tokens of noise.

## What it does

Underpass Runtime gives AI agents **isolated workspaces** with **99 governed tools** — filesystem, git, build, test, security scans, containers, Kubernetes — all under policy enforcement with full telemetry.

Every tool recommendation is scored by a **4-tier algorithm stack** that learns from execution outcomes:

```
n < 10    Heuristic scoring (risk, cost, hint matching)
n < 50    Heuristic + learned policy (fixed confidence boost)
n < 100   Thompson Sampling (Beta-distribution explore/exploit)
n >= 100  Neural Thompson Sampling (MLP with weight perturbation)
```

The entire decision is **auditable**: every recommendation emits a NATS event, persists a `RecommendationDecision`, and exposes `algorithm_id`, `decision_source`, and `policy_mode` in the response.

## Architecture

```
                    +----------------------------------+
  NATS event ------>|      Agent (specialized)         |
                    +---------------+------------------+
                                    | gRPC/mTLS
                    +---------------v------------------+
                    |       Underpass Runtime           |
                    |                                   |
                    |  Sessions --- Tool Catalog         |
                    |  Policy ----- Invocation Engine    |
                    |  Artifacts -- Telemetry            |
                    |  Recommender  Evidence Plane       |
                    +--+--------+--------+--------+----+
                       |        |        |        |
                    Valkey    NATS    S3/MinIO   OTLP
                  (state +  (events) (artifacts) (traces)
                   policies)
```

Transport: **gRPC over mTLS** (TLS 1.3 minimum) across all 5 transports. Helm chart with cert-gen hooks and fail-fast validation.

## Recommendation algorithm stack

| Algorithm | Activation | What it does |
|-----------|-----------|--------------|
| **Heuristic** | Always | Static scoring: risk penalty, cost penalty, task hint matching |
| **Telemetry boost** | When stats available | Adjusts for success rate, p95 latency, deny rate |
| **Thompson Sampling** | n >= 50 | Beta(alpha, beta) sampling for explore/exploit balance |
| **Neural Thompson Sampling** | n >= 100 + trained model | 2-layer MLP (17-dim features) with last-layer weight perturbation |

Algorithm selection is **automatic and observable**. The active scorer is visible in every recommendation response via `algorithm_id` and `decision_source`. See [Algorithm Architecture](docs/ARCHITECTURE_ALGORITHMS.md) for details.

## Proven in production-style E2E

15 E2E tests run as Kubernetes Jobs via `helm test` against a live cluster with mTLS:

| Test | What it proves |
|------|---------------|
| **Multi-agent pipeline** | 5 agents (architect > developer > test > review > QA) implement HTTP retry middleware. 14 invocations, 10 NATS events |
| **Event-driven agent** | NATS event triggers code-review agent. Full NATS round-trip with JetStream |
| **Full infra stack** | TLS + Valkey persistence + NATS JetStream events + S3 artifacts |
| **Recommendation evidence** | Bridge fields, persisted decision, evidence bundle, NATS event correlation |
| **Policy-driven recommendations** | Seeds policy in Valkey, verifies `decision_source=heuristic_with_learned_policy` |
| **Neural TS recommendations** | Seeds MLP model + policies, verifies `algorithm_id=neural_thompson_sampling` |
| **LLM agent loop** | OpenAI gpt-4o-mini drives discovery + invocation over gRPC/TLS |

See [e2e/README.md](e2e/README.md) for full evidence.

## Quick start

```bash
# Run locally (memory backends, no infra needed)
go run ./cmd/workspace

# Health check
grpcurl -plaintext localhost:50053 underpass.runtime.v1.HealthService/Check

# Deploy with Helm (full mTLS + Valkey + NATS)
helm install underpass-runtime charts/underpass-runtime \
  --set certGen.enabled=true \
  --set stores.backend=valkey \
  --set valkey.enabled=true \
  --set eventBus.type=nats \
  -f charts/underpass-runtime/values.shared-infra.yaml \
  -f charts/underpass-runtime/values.mtls.example.yaml

# Run all E2E tests
helm test underpass-runtime --timeout 10m
```

## Tool catalog

99 capabilities across 23 families:

| Family | Count | Examples |
|--------|------:|---------|
| `fs.*` | 10 | read, write, search, patch, stat, copy, move, delete |
| `git.*` | 11 | status, diff, commit, push, log, branch, checkout, apply_patch |
| `repo.*` | 14 | detect, build, test, coverage, symbols, static_analysis, package |
| `k8s.*` | 8 | get_pods, apply_manifest, rollout, logs, services, deployments |
| `redis.*` | 7 | get, set, del, scan, mget, exists, ttl |
| `node.*` | 5 | build, install, lint, test, typecheck |
| `container.*` | 4 | run, exec, logs, ps |
| `go.*` | 4 | build, test, generate, mod.tidy |
| `rust.*` | 4 | build, test, clippy, format |
| `security.*` | 4 | scan_dependencies, scan_secrets, scan_container, license_check |
| `artifact.*` | 3 | upload, download, list |
| `image.*` | 3 | build, push, inspect |
| `kafka.*` | 3 | produce, consume, topic_metadata |
| `nats.*` | 3 | publish, request, subscribe_pull |
| `python.*` | 3 | test, install_deps, validate |
| `rabbit.*` | 3 | publish, consume, queue_info |
| `c.*` | 2 | build, test |
| `conn.*` | 2 | list_profiles, describe_profile |
| `mongo.*` | 2 | find, aggregate |
| `api.*` | 1 | benchmark |
| `ci.*` | 1 | run_pipeline |
| `quality.*` | 1 | gate |
| `sbom.*` | 1 | generate |

Each tool carries metadata: scope, side_effects, risk_level, requires_approval, idempotency, cost_hint.

## gRPC API

Transport: gRPC over TLS (port 50053). Proto definitions in `specs/underpass/runtime/v1/runtime.proto`.

| Service | RPC | Description |
|---------|-----|-------------|
| HealthService | Check | Health check |
| SessionService | CreateSession | Create workspace session |
| SessionService | CloseSession | Close session |
| CapabilityCatalogService | ListTools | List available tools |
| CapabilityCatalogService | DiscoverTools | Filtered tool discovery |
| CapabilityCatalogService | RecommendTools | Ranked recommendations with evidence |
| InvocationService | InvokeTool | Execute tool in workspace |
| InvocationService | GetInvocation | Get invocation result |
| InvocationService | GetInvocationLogs | Get execution logs |
| InvocationService | GetInvocationArtifacts | List artifacts |
| LearningEvidenceService | GetRecommendationDecision | Persisted decision evidence |
| LearningEvidenceService | GetEvidenceBundle | Compact audit package |

## Part of Underpass AI

| Repository | What it does |
|-----------|-------------|
| **underpass-runtime** (this) | Governed tool execution + telemetry + adaptive learning |
| [rehydration-kernel](https://github.com/underpass-ai/rehydration-kernel) | Surgical context materialization from knowledge graphs |
| [swe-ai-fleet](https://github.com/underpass-ai/swe-ai-fleet) | Multi-agent SWE platform — planning, deliberation, execution |
| [underpass-demo](https://github.com/underpass-ai/underpass-demo) | See it all working together |

## Documentation

- [Algorithm Architecture](docs/ARCHITECTURE_ALGORITHMS.md) — 4-tier scoring stack, NeuralTS, selection logic
- [Evidence Plane](docs/EVIDENCE_PLANE.md) — recommendation traceability and audit
- [Configuration Reference](docs/CONFIGURATION.md) — 80+ environment variables
- [TLS Deployment Guide](docs/DEPLOYMENT-TLS.md) — mTLS for all 5 transports
- [Helm Install Guide](docs/HELM_INSTALL.md) — production deployment with cert-gen
- [CI Automation](docs/CI_AUTOMATION.md) — image builds, registry, versioning
- [E2E Test Evidence](e2e/README.md) — 15 tests with `helm test`

## License

Apache License 2.0 — see [LICENSE](LICENSE).

Created by [Tirso Garcia](https://github.com/tgarciai) · [LinkedIn](https://www.linkedin.com/in/tirsogarcia/) · [Underpass AI](https://github.com/underpass-ai)
