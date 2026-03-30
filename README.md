# Underpass Runtime

[![CI](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg)](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml)
[![CodeQL](https://github.com/underpass-ai/underpass-runtime/actions/workflows/ci.yml/badge.svg?event=push)](https://github.com/underpass-ai/underpass-runtime/security/code-scanning)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev)

**Governed execution plane for event-driven AI agents.**

We don't build models. We build the infrastructure that makes them actually work. A 7B model with 394 tokens of surgical context outperforms a frontier model drowning in 6,000 tokens of noise.

## What it does

Underpass Runtime gives AI agents **isolated workspaces** with **99 governed tools** — filesystem, git, build, test, security scans, containers, Kubernetes — all under policy enforcement with full telemetry.

When an event fires (task assigned, PR opened, build broken), a specialized agent activates, gets only the context it needs, selects the best tools via Thompson Sampling, and executes them in a governed workspace. The telemetry feeds back into the learning loop. No polling. No orchestrator.

```
NATS event → agent activates → session created →
  tools selected (Thompson Sampling) → executed in isolated workspace →
    telemetry recorded → policies improve → next event, better decisions
```

## Architecture

```
                    ┌─────────────────────────────────┐
  NATS event ──────>│         Agent (specialized)      │
                    └──────────────┬──────────────────┘
                                   │ HTTPS/TLS
                    ┌──────────────▼──────────────────┐
                    │      Underpass Runtime           │
                    │                                  │
                    │  Sessions ──── Tool Catalog      │
                    │  Policy ────── Invocation Engine  │
                    │  Artifacts ─── Telemetry          │
                    └──┬────────┬────────┬────────┬───┘
                       │        │        │        │
                    Valkey    NATS    S3/MinIO   OTLP
                  (state)  (events) (artifacts) (traces)
```

Full TLS across all 5 transports. Helm chart with mTLS support and fail-fast validation.

## Proven in production-style E2E

14 E2E tests run as Kubernetes Jobs against a live cluster with TLS enabled:

| Test | What it proves |
|------|---------------|
| **Multi-agent pipeline** | 5 agents (architect → developer → test → review → QA) implement an HTTP retry middleware in Go. 14 tool invocations, 10 NATS events, 6 real artifacts |
| **Event-driven agent** | NATS event triggers code-review agent. Writes Go code with a known bug, analyzes it, produces review with 3 findings. Full NATS round-trip |
| **Full infra stack** | TLS + Valkey persistence + NATS events + outbox relay + S3 artifacts — all working together |
| **LLM agent loop** | OpenAI gpt-4o-mini drives tool discovery + invocation over HTTPS. Creates a Go project in 5 iterations |

See [e2e/README.md](e2e/README.md) for full evidence.

## Quick start

```bash
# Run locally (memory backends, no infra needed)
go run ./cmd/workspace

# Health check
curl http://localhost:50053/healthz

# Create a session and invoke a tool
curl -X POST http://localhost:50053/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"principal":{"tenant_id":"dev","actor_id":"me","roles":["developer"]}}'

# Deploy with Helm (TLS + Valkey + NATS)
helm install underpass-runtime charts/underpass-runtime \
  --set stores.backend=valkey \
  --set valkey.enabled=true \
  --set eventBus.type=nats \
  --set tls.mode=server \
  --set tls.existingSecret=my-tls-secret
```

## Tool catalog

99 capabilities across 23 families:

| Family | Tools | Examples |
|--------|-------|---------|
| `fs.*` | File operations | read, write, search, patch, stat, tree |
| `git.*` | Version control | status, diff, commit, push, branch, log |
| `repo.*` | Project analysis | detect, build, test, coverage, symbols |
| `security.*` | Supply chain | scan, sbom, license audit, secret detection |
| `k8s.*` | Kubernetes | get, apply, rollout, logs, describe |
| `image.*` | Containers | build, push, inspect |
| `conn.*` | Connections | profile discovery |
| `nats.*` `kafka.*` `rabbit.*` | Messaging | governed publish/subscribe |
| `redis.*` `mongo.*` | Data | governed queries |
| `artifact.*` | Storage | upload, download |

Each tool carries metadata: scope, side_effects, risk_level, requires_approval, idempotency, cost_hint.

## API

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/healthz` | Health check |
| `GET` | `/metrics` | Prometheus metrics |
| `POST` | `/v1/sessions` | Create workspace session |
| `DELETE` | `/v1/sessions/{id}` | Close session |
| `GET` | `/v1/sessions/{id}/tools` | List tools |
| `GET` | `/v1/sessions/{id}/tools/discovery` | Discover tools (filtered) |
| `GET` | `/v1/sessions/{id}/tools/recommendations` | Tool recommendations |
| `POST` | `/v1/sessions/{id}/tools/{name}/invoke` | Invoke tool |
| `GET` | `/v1/invocations/{id}` | Get invocation |
| `GET` | `/v1/invocations/{id}/logs` | Get logs |
| `GET` | `/v1/invocations/{id}/artifacts` | List artifacts |

## Part of Underpass AI

| Repository | What it does |
|-----------|-------------|
| **underpass-runtime** (this) | Governed tool execution + telemetry + tool-learning |
| [rehydration-kernel](https://github.com/underpass-ai/rehydration-kernel) | Surgical context materialization from knowledge graphs |
| [swe-ai-fleet](https://github.com/underpass-ai/swe-ai-fleet) | Multi-agent SWE platform — planning, deliberation, execution |
| [underpass-demo](https://github.com/underpass-ai/underpass-demo) | See it all working together |

## Documentation

- [Configuration Reference](docs/CONFIGURATION.md) — 80+ environment variables
- [TLS Deployment Guide](docs/DEPLOYMENT-TLS.md) — step-by-step for all 5 transports
- [E2E Test Evidence](e2e/README.md) — cluster validation with logs and evidence JSON

## License

Apache License 2.0 — see [LICENSE](LICENSE).

Created by [Tirso Garcia](https://github.com/tgarciai) · [LinkedIn](https://www.linkedin.com/in/tirsogarcia/) · [Underpass AI](https://github.com/underpass-ai)
