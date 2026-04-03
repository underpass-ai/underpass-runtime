# Documentation Index

## Architecture

| Document | Description |
|---|---|
| [Security Model](security-model.md) | Trust boundaries, threat model, authorization layers, known gaps |
| [ADR-001: Hexagonal Architecture](adr/ADR-001-hexagonal-architecture-in-go.md) | Package layout, port/adapter design, dependency injection |
| [ADR-002: YAML Tool Catalog](adr/ADR-002-yaml-tool-catalog.md) | Why tool metadata lives in embedded YAML |
| [ADR-003: Thompson Sampling](adr/ADR-003-thompson-sampling-tool-recommendations.md) | Online heuristic + offline learning pipeline |
| [Runtime Tool Learning Audit](RUNTIME_TOOL_LEARNING_AUDIT.md) | Code-based audit of the current recommendation and learning path |
| [Runtime Tool Learning Traceability API](RUNTIME_TOOL_LEARNING_TRACEABILITY_API.md) | Event-first evidence plane and audit API proposal |
| [Runtime Tool Learning Agent Handoff](RUNTIME_TOOL_LEARNING_AGENT_HANDOFF_2026-04-02.md) | Implementation brief and execution order for the runtime agent |

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

## Infrastructure

| Document | Description |
|---|---|
| [Observability Stack](https://github.com/underpass-ai/underpass-observability) | Grafana, Loki, OTEL Collector, Prometheus, alert-relay (separate repo) |
| [Algorithm Architecture](ARCHITECTURE_ALGORITHMS.md) | Scoring tiers, NeuralTS, explainability trace, cross-agent learning |
| [Evidence Plane](EVIDENCE_PLANE.md) | Recommendation traceability, decision store, feedback loop |

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
| [OpenAPI 3.1 — Learning Evidence API](../api/openapi/learning.v1.yaml) | Read-only evidence and auditability API |
| [AsyncAPI 3.0 — Learning Events](../api/asyncapi/learning-events.v1.yaml) | Recommendation and tool-learning event contract |

## Reference

| Document | Description |
|---|---|
| [Capability Catalog](CAPABILITY_CATALOG.md) | Auto-generated catalog of 99 tools |
| [Tool Catalog Guide](TOOL_CATALOG_GUIDE.md) | How to add new tools |
| [Runner Images](RUNNER_IMAGES.md) | 6 runner profiles (base, toolchains, secops, container, k6, fat) |
| [vLLM Setup](VLLM_SETUP.md) | vLLM integration for LLM-driven agents |
