# Documentation Index

## Architecture

| Document | Description |
|---|---|
| [Security Model](security-model.md) | Trust boundaries, threat model, authorization layers, known gaps |
| [ADR-001: Hexagonal Architecture](adr/ADR-001-hexagonal-architecture-in-go.md) | Package layout, port/adapter design, dependency injection |
| [ADR-002: YAML Tool Catalog](adr/ADR-002-yaml-tool-catalog.md) | Why tool metadata lives in embedded YAML |
| [ADR-003: Thompson Sampling](adr/ADR-003-thompson-sampling-tool-recommendations.md) | Online heuristic + offline learning pipeline |

## Operations

| Document | Description |
|---|---|
| [Kubernetes Deployment](operations/kubernetes-deploy.md) | Step-by-step deployment guide (minimal → production) |
| [Cluster Prerequisites](operations/cluster-prerequisites.md) | Required/optional cluster components, resource estimates |
| [TLS Deployment](DEPLOYMENT-TLS.md) | TLS across all 5 transports (HTTP, Valkey, NATS, S3, OTLP) |
| [Configuration Reference](CONFIGURATION.md) | Complete environment variable reference |

## Testing

| Document | Description |
|---|---|
| [Testing Guide](testing.md) | Test pyramid, unit/integration/E2E matrix, CI gates |
| [Observability](observability.md) | Metrics inventory, OTel tracing, Prometheus alerts, Grafana queries |

## Runbooks

| Document | Description |
|---|---|
| [Incident Response](runbooks/incident-response.md) | Triage, diagnosis, escalation |
| [Scaling](runbooks/scaling.md) | HPA, vertical scaling, capacity planning |
| [TLS Certificate Rotation](runbooks/tls-rotation.md) | Zero-downtime cert renewal |

## API Contracts

| Document | Description |
|---|---|
| [OpenAPI 3.1 — Workspace API](../api/openapi/workspace.v1.yaml) | HTTP API contract (sessions, tools, invocations) |
| [AsyncAPI 3.0 — Domain Events](../api/asyncapi/workspace-events.v1.yaml) | NATS event contract (6 event types) |

## Reference

| Document | Description |
|---|---|
| [Capability Catalog](CAPABILITY_CATALOG.md) | Auto-generated catalog of 99 tools |
| [Tool Catalog Guide](TOOL_CATALOG_GUIDE.md) | How to add new tools |
| [Runner Images](RUNNER_IMAGES.md) | 6 runner profiles (base, toolchains, secops, container, k6, fat) |
| [vLLM Setup](VLLM_SETUP.md) | vLLM integration for LLM-driven agents |
