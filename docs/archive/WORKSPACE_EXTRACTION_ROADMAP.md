# Workspace Service — Extraction & Production Roadmap

> **Branch**: `plan/workspace-extraction-roadmap`
> **Date**: 2026-03-07
> **Status**: Planning

---

## Table of Contents

1. [Current State Assessment](#1-current-state-assessment)
2. [Gap Analysis](#2-gap-analysis)
3. [Backlog — Phase 1: Standalone Extraction](#3-phase-1--standalone-extraction)
4. [Backlog — Phase 2: Docker-First Runtime](#4-phase-2--docker-first-runtime)
5. [Backlog — Phase 3: Event-Driven Architecture](#5-phase-3--event-driven-architecture)
6. [Backlog — Phase 4: Discovery & Recommendations](#6-phase-4--discovery--recommendations)
7. [Backlog — Phase 5: Rehidratacion & Portability](#7-phase-5--rehidratacion--portability)
8. [Backlog — Phase 6: Telemetry & Learning Loop](#8-phase-6--telemetry--learning-loop)
9. [Dependency Graph](#9-dependency-graph)
10. [Glossary](#10-glossary)

---

## 1. Current State Assessment

### What already works

| Area | Detail |
|------|--------|
| **Core execution** | `CreateSession`, `ListTools`, `InvokeTool`, `GetInvocation`, `GetInvocationLogs`, `GetInvocationArtifacts` — all via HTTP/JSON |
| **Governance** | Per-capability metadata: scope, side_effects, risk_level, requires_approval, idempotency, constraints, observability, policy fields |
| **Policy enforcement** | Static policy engine with field-level validation (paths, args, profiles, subjects, topics, queues, key prefixes, namespaces, registries) |
| **Quota & dedup** | Rate limits, concurrency slots, output/artifact size limits, correlation-ID deduplication |
| **Audit** | Every invocation recorded with actor, tenant, status, metadata |
| **Observability** | OpenTelemetry tracing, Prometheus metrics, slog structured logging |
| **Tool catalog** | 96 capabilities across 15+ families (fs, git, repo, artifact, image, security, sbom, license, deps, secrets, nats, kafka, rabbit, redis, mongo, k8s, api, container, language toolchains) loaded from embedded YAML |
| **Persistence** | SessionStore + InvocationStore: memory or Valkey. ArtifactStore: local FS with SHA256 hash |
| **K8s runtime** | Runner pod per session, runner profiles/image bundles, init containers, pod janitor, security context (non-root, drop caps), git auth secrets |
| **Hexagonal arch** | Clean separation: domain → app (interfaces) → adapters → httpapi → cmd wiring |

### Key files

| Role | Path |
|------|------|
| Wiring | `cmd/workspace/main.go` |
| Business logic | `internal/app/service.go` |
| Contracts | `internal/app/types.go` |
| Domain types | `internal/domain/capability.go`, `session.go`, `invocation.go` |
| Tool handlers | `internal/adapters/tools/*.go` (60+ files) |
| Tool catalog | `internal/adapters/tools/catalog_defaults.yaml` (96 tools) |
| Policy | `internal/adapters/policy/static_policy.go` |
| Workspace backends | `internal/adapters/workspace/local_manager.go`, `kubernetes_manager.go` |
| Stores | `internal/adapters/sessionstore/`, `invocationstore/`, `storage/` |
| HTTP API | `internal/httpapi/server.go` |
| Build | `Makefile`, `Dockerfile`, root `docker-compose.yml` |

---

## 2. Gap Analysis

### P0 — Bloqueantes

| ID | Gap | Impact |
|----|-----|--------|
| P0.1 | **Module path coupled**: `github.com/underpass-ai/swe-ai-fleet/services/workspace` | Not publishable as independent micro |
| P0.2 | **K8s always compiled**: no build tags, `k8s.io/*` in 14 files, binary always includes K8s client | No community build without K8s deps |
| P0.3 | **No Docker runtime**: only `local` (os/exec) and `kubernetes` (pod exec). Container tools return simulated responses when K8s unavailable | No real container isolation outside K8s |
| P0.4 | **No internal event bus**: audit is fire-and-forget to slog. No `EventPublisher` port, no domain events, no outbox | No event-driven integration with fleet |

### P1 — Production blockers

| ID | Gap | Impact |
|----|-----|--------|
| P1.1 | **ArtifactStore only local FS** | No portability outside original node |
| P1.2 | **SessionStore local = in-memory** | Service restart loses session index, leaves orphan workspaces |
| P1.3 | **Monolithic tool wiring** in main.go (lines 94-200, 100+ handler instantiations) | Hard to customize, no bundle concept |
| P1.4 | **No discovery API for LLM** | `GET /tools` returns full list, no compact view, no filters, no ranking |
| P1.5 | **No learning loop** | Metrics exist but no telemetry store, no recommendation engine, no feedback |

### P2 — Hardening

| ID | Gap | Impact |
|----|-----|--------|
| P2.1 | Secrets/PII redaction not formalized | Audit/telemetry may leak sensitive data |
| P2.2 | No supply chain attestation for runner images | Unsigned images in production |
| P2.3 | No multi-tenant isolation classes | Single namespace, no org-level quotas |

### Hardcoded `swe-ai-fleet` references

| Location | Default | Env override |
|----------|---------|-------------|
| `kubernetes_manager.go:23` | `defaultK8sNamespace = "swe-ai-fleet"` | `WORKSPACE_K8S_NAMESPACE` |
| `cmd/workspace/main.go:40` | `defaultNamespace = "swe-ai-fleet"` | `WORKSPACE_K8S_NAMESPACE` |
| `cmd/workspace/main.go:272,309` | `valkey.swe-ai-fleet.svc.cluster.local` | `VALKEY_HOST` |
| `Makefile` docker-build | `registry.underpassai.com/swe-ai-fleet/workspace` | — |
| `docker-compose.yml` | `swe-workspace` container name | — |
| `Dockerfile` | Copies from `services/workspace/` (monorepo layout) | — |

---

## 3. Phase 1 — Standalone Extraction

> **Goal**: Decouple module, neutralize defaults, standalone Dockerfile. No functional changes.

### WS-EXT-001: Change Go module path

| Field | Value |
|-------|-------|
| **Objective** | Replace `github.com/underpass-ai/swe-ai-fleet/services/workspace` with `github.com/underpass-ai/workspace-service` |
| **Files** | `go.mod`, every `.go` file with internal imports (~100+) |
| **DoD** | `go build ./...` passes; `go test ./...` passes; no import references to old module path |
| **Risk** | Low — mechanical find-replace. Must not break `swe-ai-fleet` consumers until integration PR |
| **Depends on** | None |
| **Notes** | Run `go mod edit -module github.com/underpass-ai/workspace-service` + `gofmt -w -r` or `sed` for imports. Verify with `go vet ./...` |

### WS-EXT-002: Neutralize hardcoded defaults

| Field | Value |
|-------|-------|
| **Objective** | Remove all `swe-ai-fleet` references from defaults. Use neutral values: namespace `workspace`, Valkey host `localhost:6379`, image prefix `workspace-service` |
| **Files** | `cmd/workspace/main.go` (lines 40, 272, 309), `internal/adapters/workspace/kubernetes_manager.go` (line 23), `Makefile` (docker-build target) |
| **DoD** | `grep -r "swe-ai-fleet" services/workspace/` returns zero hits (excluding docs/comments). All defaults are environment-neutral |
| **Risk** | Low — existing env overrides still work. Document the migration for swe-ai-fleet integration |
| **Depends on** | None |

### WS-EXT-003: Standalone Dockerfile

| Field | Value |
|-------|-------|
| **Objective** | Dockerfile works from repo root without monorepo context. Self-contained `COPY . .` or multi-stage with only workspace files |
| **Files** | `Dockerfile` |
| **DoD** | `docker build -t workspace-service .` works from the workspace-service repo root. No `services/workspace/` prefix in COPY instructions |
| **Risk** | Low |
| **Depends on** | WS-EXT-001 |

### WS-EXT-004: Standalone docker-compose

| Field | Value |
|-------|-------|
| **Objective** | Create `docker-compose.yml` for standalone usage (workspace + Valkey) and `docker-compose.full.yml` (+ NATS, if event bus enabled) |
| **Files** | New: `docker-compose.yml`, `docker-compose.full.yml` |
| **DoD** | `docker compose up` starts workspace-service with local backend. Health check passes. E2E smoke test passes against it |
| **Risk** | Low |
| **Depends on** | WS-EXT-003 |

### WS-EXT-005: Integration shim for swe-ai-fleet

| Field | Value |
|-------|-------|
| **Objective** | In swe-ai-fleet repo, replace `services/workspace/` with a go.mod `replace` directive or git submodule pointing to the new repo. Update root docker-compose.yml and CI |
| **Files** | swe-ai-fleet: `docker-compose.yml`, CI workflows, root Makefile includes |
| **DoD** | `make service-test-core SERVICE=workspace` still passes in swe-ai-fleet CI. docker-compose brings up workspace with fleet-specific env overrides |
| **Risk** | Medium — coordinated change across two repos |
| **Depends on** | WS-EXT-001, WS-EXT-003 |

---

## 4. Phase 2 — Docker-First Runtime

> **Goal**: Real container isolation without K8s. Community-first deployment model.

### WS-RT-001: K8s behind build tags

| Field | Value |
|-------|-------|
| **Objective** | Move all K8s-dependent code behind `//go:build k8s` build tag. Default build produces a binary with zero `k8s.io/*` dependencies |
| **Files** | 14 files with k8s imports: `kubernetes_manager.go`, `kubernetes_manager_test.go`, `kubernetes_pod_janitor.go`, `kubernetes_pod_janitor_test.go`, `k8s_tools.go`, `k8s_tools_test.go`, `k8s_delivery_tools.go`, `k8s_delivery_tools_test.go`, `container_tools.go` (K8s-aware constructors), `runner.go` (RoutingCommandRunner + K8sCommandRunner), `runner_test.go`, `cmd/workspace/main.go` (K8s wiring block) |
| **DoD** | `go build ./cmd/workspace` succeeds without k8s tag. `go build -tags k8s ./cmd/workspace` includes K8s support. `go.mod` no longer pulls `k8s.io/*` in default build (use `go mod graph` to verify) |
| **Risk** | High — requires splitting files and introducing registration patterns for K8s adapters. Must not break existing K8s users |
| **Depends on** | None (can run in parallel with Phase 1) |
| **Notes** | Pattern: create `k8s_register.go` with `//go:build k8s` that calls `init()` to register K8s backend factory. Default build uses `k8s_register_noop.go` |

### WS-RT-002: Capability bundle registry

| Field | Value |
|-------|-------|
| **Objective** | Replace monolithic handler instantiation in `main.go` (lines 94-200) with a `ToolsetRegistry` that registers bundles: `core`, `repo`, `secops`, `messaging`, `data`, `image`, `k8s` |
| **Files** | New: `internal/bootstrap/registry.go`, `internal/bootstrap/core.go`, `internal/bootstrap/repo.go`, `internal/bootstrap/secops.go`, `internal/bootstrap/messaging.go`, `internal/bootstrap/data.go`, `internal/bootstrap/image.go`, `internal/bootstrap/k8s.go` (build-tagged). Modified: `cmd/workspace/main.go` |
| **DoD** | `main.go` wiring reduced to `registry.RegisterAll(cfg)` + `registry.BuildEngine()`. Individual bundles can be excluded via build tags or config. `go test ./...` passes |
| **Risk** | Medium — refactor of core wiring. Must maintain 100% backward compatibility |
| **Depends on** | WS-RT-001 |

### WS-RT-003: Docker runtime backend

| Field | Value |
|-------|-------|
| **Objective** | Implement `WORKSPACE_BACKEND=docker` with 1 container per session, toolset image allowlist, resource limits, TTL janitor |
| **Files** | New: `internal/adapters/workspace/docker_manager.go`, `docker_manager_test.go`, `internal/adapters/tools/docker_runner.go`, `docker_runner_test.go` |
| **DoD** | `WORKSPACE_BACKEND=docker` creates a Docker container per session. Commands execute inside the container. Container is cleaned up on session close or TTL expiry. Resource limits (CPU, memory) enforced. Fake Docker client for unit tests (same pattern as K8s fakes) |
| **Risk** | High — Docker socket access implies security surface. Document rootless Podman alternative |
| **Depends on** | WS-RT-002 (uses ToolsetRegistry for image selection) |

**Interface contract**:

```go
// DockerManager implements WorkspaceManager
type DockerManager struct {
    client         DockerClient          // interface for testability
    sessionStore   app.SessionStore
    imageAllowlist map[string]bool
    defaultImage   string
    resourceLimits ResourceLimits
    ttl            time.Duration
}

// DockerCommandRunner implements CommandRunner
type DockerCommandRunner struct {
    client DockerClient
}
```

**Config env vars**:

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKSPACE_DOCKER_IMAGE` | `alpine:3.20` | Default runner image |
| `WORKSPACE_DOCKER_IMAGE_BUNDLES_JSON` | `{}` | Profile-based image map |
| `WORKSPACE_DOCKER_CPU_LIMIT` | `2` | CPU cores limit |
| `WORKSPACE_DOCKER_MEMORY_LIMIT` | `2Gi` | Memory limit |
| `WORKSPACE_DOCKER_TTL_SECONDS` | `3600` | Session container TTL |
| `WORKSPACE_DOCKER_NETWORK` | `workspace-net` | Docker network |
| `WORKSPACE_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket path |

### WS-RT-004: Container tools real implementation

| Field | Value |
|-------|-------|
| **Objective** | Replace simulated container tool responses with real Docker/Podman execution when `WORKSPACE_BACKEND=docker` |
| **Files** | `internal/adapters/tools/container_tools.go` |
| **DoD** | `container.ps`, `container.logs`, `container.run`, `container.exec` work against real Docker daemon in docker backend. K8s backend behavior unchanged. Local backend returns clear "not supported" error |
| **Risk** | Medium |
| **Depends on** | WS-RT-003 |

---

## 5. Phase 3 — Event-Driven Architecture

> **Goal**: Workspace service publishes domain events. Compatible with swe-ai-fleet NATS JetStream. Standalone mode uses noop bus.

### WS-EV-001: Domain event types

| Field | Value |
|-------|-------|
| **Objective** | Define domain event types for workspace lifecycle |
| **Files** | New: `internal/domain/events.go` |
| **DoD** | Types defined with JSON tags, version field, timestamp. Documented in code comments |
| **Risk** | Low |
| **Depends on** | None |

**Events V1**:

```go
type EventType string

const (
    EventSessionCreated       EventType = "workspace.session.created"
    EventSessionClosed        EventType = "workspace.session.closed"
    EventInvocationStarted    EventType = "workspace.invocation.started"
    EventInvocationCompleted  EventType = "workspace.invocation.completed"
    EventArtifactStored       EventType = "workspace.artifact.stored"
)

type DomainEvent struct {
    ID        string          `json:"id"`
    Type      EventType       `json:"type"`
    Version   string          `json:"version"`     // "v1"
    Timestamp time.Time       `json:"timestamp"`
    SessionID string          `json:"session_id"`
    TenantID  string          `json:"tenant_id"`
    ActorID   string          `json:"actor_id"`
    Payload   json.RawMessage `json:"payload"`
}
```

### WS-EV-002: EventPublisher port + noop adapter

| Field | Value |
|-------|-------|
| **Objective** | Add `EventPublisher` interface to app ports. Default adapter = noop (log only). Wire into `Service` struct |
| **Files** | `internal/app/types.go` (new interface), new: `internal/adapters/eventbus/noop.go`, `internal/adapters/eventbus/noop_test.go`. Modified: `internal/app/service.go` (publish calls at session create/close, invocation start/complete, artifact store) |
| **DoD** | `Service.InvokeTool()` publishes `InvocationStarted` before execution and `InvocationCompleted` after. `CreateSession` publishes `SessionCreated`. Noop adapter logs events at debug level. No functional change to existing behavior |
| **Risk** | Low — noop is default, no external dependency |
| **Depends on** | WS-EV-001 |

**Interface**:

```go
type EventPublisher interface {
    Publish(ctx context.Context, event domain.DomainEvent) error
}
```

### WS-EV-003: Outbox store

| Field | Value |
|-------|-------|
| **Objective** | Implement transactional outbox pattern. Events written to outbox before publish. Background relay reads outbox and publishes to bus. Guarantees at-least-once delivery |
| **Files** | New: `internal/adapters/eventbus/outbox.go`, `outbox_test.go`, `outbox_relay.go` |
| **DoD** | Outbox persists events to Valkey list. Relay goroutine drains outbox and calls `EventPublisher.Publish()`. Relay handles failures with exponential backoff. Unit tests with fake Valkey |
| **Risk** | Medium — must handle relay crashes, duplicate delivery |
| **Depends on** | WS-EV-002 |

### WS-EV-004: NATS JetStream publisher

| Field | Value |
|-------|-------|
| **Objective** | Implement `EventPublisher` adapter for NATS JetStream. Subject pattern: `workspace.events.{event_type}` |
| **Files** | New: `internal/adapters/eventbus/nats_publisher.go`, `nats_publisher_test.go` |
| **DoD** | `EVENT_BUS=nats` activates NATS publisher. Events published to JetStream with message deduplication (event ID as Nats-Msg-Id). Connection retry on failure. Unit tests with fake NATS client |
| **Risk** | Medium — JetStream stream must exist (auto-create or require pre-provisioning) |
| **Depends on** | WS-EV-002 |
| **Config** | `EVENT_BUS=none\|nats`, `EVENT_BUS_NATS_URL`, `EVENT_BUS_NATS_STREAM` |

---

## 6. Phase 4 — Discovery & Recommendations

> **Goal**: LLM-optimized tool discovery. Reduce context consumption. Enable task-aware ranking.

### WS-DSC-001: Compact discovery endpoint

| Field | Value |
|-------|-------|
| **Objective** | New endpoint `GET /v1/sessions/{id}/tools/discovery?detail=compact` returns minimal tool metadata optimized for LLM prompt injection |
| **Files** | `internal/httpapi/server.go` (new route), `internal/app/service.go` (new method `DiscoverTools`) |
| **DoD** | Returns: name, short_description (max 120 chars), args_schema_lite (required fields only), risk_level, side_effects, requires_approval, toolset_tags, cost_hint. Response size < 30% of full ListTools. Unit test + integration test |
| **Risk** | Low |
| **Depends on** | None |

**Response schema**:

```json
{
  "tools": [
    {
      "name": "fs.read",
      "description": "Read file contents from workspace",
      "required_args": ["path"],
      "risk": "low",
      "side_effects": "none",
      "approval": false,
      "tags": ["core", "fs"],
      "cost": "cheap"
    }
  ],
  "total": 96,
  "filtered": 42
}
```

### WS-DSC-002: Discovery filters

| Field | Value |
|-------|-------|
| **Objective** | Add query params: `?risk=low,medium`, `?tags=core,repo`, `?side_effects=none`, `?scope=repo`, `?runtime=local` |
| **Files** | `internal/httpapi/server.go`, `internal/app/service.go` |
| **DoD** | Filters are AND-combined. Empty filter = all. Filtered count in response. Unit tests for each filter dimension |
| **Risk** | Low |
| **Depends on** | WS-DSC-001 |

### WS-DSC-003: Full discovery endpoint

| Field | Value |
|-------|-------|
| **Objective** | `GET /v1/sessions/{id}/tools/discovery?detail=full` returns complete metadata including examples, constraints, observability, policy fields, historical stats (if available) |
| **Files** | Same as WS-DSC-001 (parameter-driven) |
| **DoD** | Full view includes all Capability fields + optional `stats` block (populated in Phase 6). Documentation-grade output |
| **Risk** | Low |
| **Depends on** | WS-DSC-001 |

### WS-DSC-004: Recommendations endpoint

| Field | Value |
|-------|-------|
| **Objective** | `GET /v1/sessions/{id}/tools/recommendations?task_hint=...&top_k=10` returns ranked tools with explanation |
| **Files** | New: `internal/app/recommender.go`, `recommender_test.go`. Modified: `internal/httpapi/server.go` |
| **DoD** | Heuristic scoring: `score = success_weight - duration_penalty - risk_penalty - approval_penalty`. Hard constraints: policy-denied = excluded, runtime-unsupported = excluded. Returns `tool_name`, `score`, `why` (human-readable reason), `estimated_cost`. Unit tests with mock telemetry |
| **Risk** | Medium — scoring weights need tuning based on real usage |
| **Depends on** | WS-DSC-001, WS-TEL-001 (Phase 6, but can ship with static heuristic first) |

**Response schema**:

```json
{
  "recommendations": [
    {
      "name": "repo.test",
      "score": 0.92,
      "why": "High success rate for Go projects, low cost, no side effects",
      "estimated_cost": "medium",
      "policy_notes": []
    }
  ],
  "task_hint": "run unit tests",
  "top_k": 10
}
```

---

## 7. Phase 5 — Rehidratacion & Portability

> **Goal**: Sessions survive restarts. Artifacts portable across nodes. Workspace snapshots for context continuity.

### WS-PRT-001: S3/MinIO artifact store

| Field | Value |
|-------|-------|
| **Objective** | Implement `ArtifactStore` adapter backed by S3-compatible storage (MinIO for self-hosted, AWS S3 for cloud) |
| **Files** | New: `internal/adapters/storage/s3_artifacts.go`, `s3_artifacts_test.go` |
| **DoD** | `ARTIFACT_BACKEND=s3` activates S3 store. Save/List/Read operations work. SHA256 integrity preserved. Configurable bucket, prefix, region. Unit tests with fake S3 client. Integration test with MinIO in docker-compose |
| **Risk** | Medium — S3 API surface is large; scope to Put/Get/List only |
| **Depends on** | None |
| **Config** | `ARTIFACT_BACKEND=local\|s3`, `ARTIFACT_S3_BUCKET`, `ARTIFACT_S3_ENDPOINT`, `ARTIFACT_S3_REGION`, `ARTIFACT_S3_PREFIX` |

### WS-PRT-002: Workspace snapshots

| Field | Value |
|-------|-------|
| **Objective** | Add `SnapshotStore` interface. Create/restore workspace snapshots (tarball of workspace dir). Enable session migration across nodes |
| **Files** | `internal/app/types.go` (new interface), new: `internal/adapters/storage/snapshot_store.go`, `snapshot_store_test.go`. Modified: `internal/app/service.go` (new methods: `CreateSnapshot`, `RestoreSnapshot`) |
| **DoD** | Snapshot creates a compressed tarball of workspace dir, stores via ArtifactStore (local or S3). Restore extracts to new workspace dir. Session metadata includes `snapshot_ref`. Unit tests |
| **Risk** | Medium — large workspaces may be slow to snapshot |
| **Depends on** | WS-PRT-001 (for S3-backed snapshots) |

**Interface**:

```go
type SnapshotStore interface {
    Create(ctx context.Context, sessionID string, workspaceDir string) (SnapshotRef, error)
    Restore(ctx context.Context, ref SnapshotRef, targetDir string) error
}

type SnapshotRef struct {
    ID        string    `json:"id"`
    SessionID string    `json:"session_id"`
    Path      string    `json:"path"`       // artifact store path
    Size      int64     `json:"size"`
    CreatedAt time.Time `json:"created_at"`
    Checksum  string    `json:"checksum"`   // SHA256
}
```

### WS-PRT-003: Context digest V1

| Field | Value |
|-------|-------|
| **Objective** | Generate a lightweight context digest from workspace state. Used by recommendations engine and LLM context injection |
| **Files** | New: `internal/app/context_digest.go`, `context_digest_test.go` |
| **DoD** | Digest includes: repo language, project type, detected frameworks, test status summary, recent tool outcomes (last N), active toolset, security posture summary. JSON-serializable. < 2KB typical. Unit tests |
| **Risk** | Low — pure read operation |
| **Depends on** | None (but most useful after WS-DSC-004 and WS-TEL-001) |

**Digest schema**:

```go
type ContextDigest struct {
    Version          string            `json:"version"`          // "v1"
    RepoLanguage     string            `json:"repo_language"`
    ProjectType      string            `json:"project_type"`
    Frameworks       []string          `json:"frameworks"`
    HasDockerfile    bool              `json:"has_dockerfile"`
    HasK8sManifests  bool              `json:"has_k8s_manifests"`
    TestStatus       string            `json:"test_status"`      // "passing"|"failing"|"unknown"
    RecentOutcomes   []OutcomeSummary  `json:"recent_outcomes"`
    ActiveToolset    []string          `json:"active_toolset"`
    SecurityPosture  string            `json:"security_posture"` // "clean"|"warnings"|"critical"
}
```

---

## 8. Phase 6 — Telemetry & Learning Loop

> **Goal**: Capture invocation telemetry features. Feed into recommendation engine. Close the learning loop.

### WS-TEL-001: Telemetry recorder

| Field | Value |
|-------|-------|
| **Objective** | Add `TelemetryRecorder` interface. Persist derived features per invocation (not raw logs). Enable offline analysis and online recommendations |
| **Files** | `internal/app/types.go` (new interface), new: `internal/adapters/telemetry/recorder.go`, `recorder_test.go`, `internal/adapters/telemetry/valkey_recorder.go` |
| **DoD** | Records: invocation_id, session_id, tool_name, tool_family, runtime_kind, repo_language, status, error_code, duration_ms, output_bytes, artifact_count, artifact_bytes, timestamp. Valkey backend with configurable TTL. Noop recorder for standalone mode. Unit tests |
| **Risk** | Low — append-only, no reads in hot path |
| **Depends on** | None |

**Telemetry record**:

```go
type TelemetryRecord struct {
    InvocationID   string        `json:"invocation_id"`
    SessionID      string        `json:"session_id"`
    ToolName       string        `json:"tool_name"`
    ToolFamily     string        `json:"tool_family"`
    ToolsetID      string        `json:"toolset_id"`
    RuntimeKind    string        `json:"runtime_kind"`
    RepoLanguage   string        `json:"repo_language"`
    ProjectType    string        `json:"project_type"`
    TenantID       string        `json:"tenant_id"`
    Approved       bool          `json:"approved"`
    Status         string        `json:"status"`
    ErrorCode      string        `json:"error_code,omitempty"`
    DurationMs     int64         `json:"duration_ms"`
    OutputBytes    int64         `json:"output_bytes"`
    LogsBytes      int64         `json:"logs_bytes"`
    ArtifactCount  int           `json:"artifact_count"`
    ArtifactBytes  int64         `json:"artifact_bytes"`
    Timestamp      time.Time     `json:"timestamp"`
}
```

### WS-TEL-002: Aggregated stats per tool

| Field | Value |
|-------|-------|
| **Objective** | Background worker aggregates telemetry into per-tool stats: success_rate, p50/p95 duration, avg output size, deny rate. Stats served by discovery full endpoint |
| **Files** | New: `internal/adapters/telemetry/aggregator.go`, `aggregator_test.go` |
| **DoD** | Stats updated every N minutes (configurable). Stored in Valkey hash per tool. Served in `?detail=full` response. Unit tests with fake recorder |
| **Risk** | Low |
| **Depends on** | WS-TEL-001, WS-DSC-003 |

### WS-TEL-003: Contextual recommendation engine

| Field | Value |
|-------|-------|
| **Objective** | Replace static heuristic in WS-DSC-004 with context-aware scoring using telemetry stats + context digest |
| **Files** | `internal/app/recommender.go` (enhance existing) |
| **DoD** | Score formula incorporates: historical success rate for current repo_language, historical duration percentile, deny rate. Context digest features weight the score. A/B comparison test showing improvement over static heuristic |
| **Risk** | Medium — cold start problem when no telemetry exists |
| **Depends on** | WS-TEL-002, WS-PRT-003 |

### WS-TEL-004: KPI dashboard metrics

| Field | Value |
|-------|-------|
| **Objective** | Expose Prometheus metrics for learning loop KPIs |
| **Files** | `internal/app/service.go` (add metric registration) |
| **DoD** | Metrics: `workspace_tool_calls_per_task`, `workspace_success_on_first_tool`, `workspace_time_to_success_seconds`, `workspace_recommendation_acceptance_rate`, `workspace_policy_denial_rate_bad_recommendation`, `workspace_context_bytes_saved`. Grafana dashboard JSON (optional) |
| **Risk** | Low |
| **Depends on** | WS-TEL-001 |

---

## 9. Dependency Graph

```
Phase 1: Standalone Extraction
  WS-EXT-001 (module path)
    └─> WS-EXT-003 (Dockerfile)
         └─> WS-EXT-004 (docker-compose)
  WS-EXT-002 (neutralize defaults)
  WS-EXT-005 (integration shim) ←── WS-EXT-001, WS-EXT-003

Phase 2: Docker-First Runtime
  WS-RT-001 (K8s build tags) ──────────────┐
    └─> WS-RT-002 (bundle registry) ───────┤
         └─> WS-RT-003 (Docker runtime) ───┤
              └─> WS-RT-004 (container tools real)

Phase 3: Event-Driven
  WS-EV-001 (domain events)
    └─> WS-EV-002 (publisher port + noop)
         ├─> WS-EV-003 (outbox)
         └─> WS-EV-004 (NATS publisher)

Phase 4: Discovery (can start after Phase 1)
  WS-DSC-001 (compact discovery) ──────────┐
    ├─> WS-DSC-002 (filters)               │
    ├─> WS-DSC-003 (full discovery)         │
    └─> WS-DSC-004 (recommendations) ──────┼── depends on WS-TEL-001 for stats
                                            │
Phase 5: Portability                        │
  WS-PRT-001 (S3 artifacts)                │
    └─> WS-PRT-002 (snapshots)             │
  WS-PRT-003 (context digest) ─────────────┘

Phase 6: Telemetry & Learning
  WS-TEL-001 (recorder) ──────────────────────────┐
    ├─> WS-TEL-002 (aggregated stats) ─── WS-DSC-003
    ├─> WS-TEL-003 (contextual reco) ──── WS-PRT-003
    └─> WS-TEL-004 (KPI metrics)
```

**Parallelization opportunities**:

- Phase 1 and Phase 2 (WS-RT-001) can run in parallel
- Phase 3 (events) is independent of Phase 2 (runtime)
- Phase 4 (discovery) can start as soon as Phase 1 is done
- Phase 5 (portability) is independent of Phases 3-4
- Phase 6 depends on Phase 4 for serving stats, but WS-TEL-001 can start early

---

## 10. Glossary

| Term | Definition |
|------|-----------|
| **Capability** | A typed tool definition with metadata (schema, risk, policy, constraints). Defined in `catalog_defaults.yaml` |
| **Tool family** | Namespace prefix of a capability (e.g., `fs`, `git`, `repo`, `k8s`) |
| **Toolset** | A curated bundle of capabilities for a specific use case (e.g., `core`, `secops`) |
| **Runner** | Execution environment for a session: process (local), container (docker), or pod (k8s) |
| **Runner profile** | Session metadata key that selects a runner image from the image bundle |
| **Outbox** | Persistent queue of domain events written atomically with state changes, relayed asynchronously to event bus |
| **Context digest** | Lightweight JSON summary of workspace state used by the recommendation engine |
| **Telemetry record** | Derived feature vector per invocation, stored for offline analysis and online ranking |
| **Discovery** | API for LLM-optimized tool catalog queries with compact/full detail levels |
