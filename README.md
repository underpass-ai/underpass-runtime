# UnderPass Runtime
### Execution plane for tool-driven agents

UnderPass Runtime provides isolated workspaces, governed tool execution, artifacts, and policy-controlled runtimes for LLM-powered agents.

Run standalone with Docker. Scale with Kubernetes. Integrate with swe-ai-fleet or use it independently.

---

## Why UnderPass Runtime

Most agent systems focus on reasoning, planning, and tool calling.

UnderPass Runtime focuses on the missing layer: the execution plane.

It gives agents:
- isolated workspaces
- governed tools with policy enforcement
- artifacts and logs as first-class outputs
- portable runtime backends
- optional event-driven integration for observability, rehydration, and future learning

---

## What it is

UnderPass Runtime is a service for executing tools inside controlled workspaces.

It is designed for tool-driven agents that need:
- filesystem access
- repository operations
- command execution
- artifacts and logs
- policy enforcement
- runtime portability

## What it is not

UnderPass Runtime is not:
- a chat interface
- an LLM orchestration framework
- a prompt management system
- a generic unrestricted shell for agents

It is the execution layer that agent systems can rely on.

---

## Core capabilities

- **Isolated workspaces** for agent sessions
- **Governed tool execution** with policy and approvals
- **Artifacts and logs** persisted per invocation
- **Multiple runtime modes** for different deployment environments
- **Structured tool catalog** with rich metadata
- **Optional event-driven integration**
- **Portable architecture** for Docker-first and Kubernetes-ready deployments

---

## Architecture

```text
Agent / Orchestrator
        |
        v
+---------------------+
| UnderPass Runtime   |
|---------------------|
| Sessions            |
| Tool Catalog        |
| Policy Enforcement  |
| Invocation Engine   |
| Artifacts & Logs    |
+---------------------+
        |
        +-------------------+
        |                   |
        v                   v
 Docker Runner         Kubernetes Runner
        |
        v
 Isolated Workspace

Optional integrations:
- NATS event bus
- Context / rehydration service
- S3-compatible artifact storage
```

---

## Quick Start

```bash
# Run locally
go run ./cmd/workspace

# Or with Docker Compose (runtime + Valkey)
docker compose up
```

## API

| Endpoint | Description |
|----------|-------------|
| `GET /healthz` | Health check |
| `GET /metrics` | Prometheus metrics |
| `POST /v1/sessions` | Create workspace session |
| `DELETE /v1/sessions/{session_id}` | Close session |
| `GET /v1/sessions/{session_id}/tools` | List available tools |
| `POST /v1/sessions/{session_id}/tools/{tool_name}/invoke` | Invoke a tool |
| `GET /v1/invocations/{invocation_id}` | Get invocation result |
| `GET /v1/invocations/{invocation_id}/logs` | Get invocation logs |
| `GET /v1/invocations/{invocation_id}/artifacts` | List invocation artifacts |

## Tool Families

96 capabilities across 15+ families:

- `fs.*` — file operations (read, write, search, patch, stat)
- `git.*` — version control (status, diff, commit, push, branch)
- `repo.*` — project analysis (detect, build, test, coverage, symbols)
- `conn.*` — connection profile discovery
- `nats.*`, `kafka.*`, `rabbit.*` — governed messaging
- `redis.*`, `mongo.*` — governed data access
- `security.*`, `sbom.*`, `license.*`, `deps.*`, `secrets.*` — supply chain
- `image.*` — container image build/push/inspect
- `k8s.*` — Kubernetes read + optional delivery tools
- `artifact.*` — artifact upload/download
- `api.benchmark` — HTTP load testing

Each tool carries metadata: scope, side_effects, risk_level, requires_approval, idempotency, constraints, observability.

Full catalog: `docs/CAPABILITY_CATALOG.md` (regenerate with `make catalog-docs`).

## Configuration

### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `50053` | HTTP listen port |
| `WORKSPACE_ROOT` | `/tmp/workspaces` | Workspace filesystem root |
| `ARTIFACT_ROOT` | `/tmp/artifacts` | Artifact storage root |
| `WORKSPACE_BACKEND` | `local` | Runtime backend: `local`, `kubernetes` |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |

### Persistence

| Variable | Default | Description |
|----------|---------|-------------|
| `SESSION_STORE_BACKEND` | `memory` | `memory` or `valkey` |
| `INVOCATION_STORE_BACKEND` | `memory` | `memory` or `valkey` |
| `VALKEY_HOST` | `localhost` | Valkey/Redis host |
| `VALKEY_PORT` | `6379` | Valkey/Redis port |

### Kubernetes Backend

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKSPACE_K8S_NAMESPACE` | `underpass-runtime` | K8s namespace |
| `WORKSPACE_K8S_RUNNER_IMAGE` | `alpine:3.20` | Runner container image |
| `WORKSPACE_ENABLE_K8S_DELIVERY_TOOLS` | `false` | Enable apply/rollout/restart |

See `docs/` for the full environment variable reference.

## Development

```bash
make build          # Build binary
make test           # Run all tests
make coverage-core  # Core coverage gate (80%)
make coverage-full  # Full coverage (SonarCloud)
make docker-build   # Container image
```

## Deployment

```bash
# Standalone with Valkey
docker compose up

# With NATS event bus
docker compose -f docker-compose.yml -f docker-compose.full.yml up
```

## License

Proprietary — UnderPass AI
