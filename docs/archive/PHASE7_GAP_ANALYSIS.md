# Phase 7 — Gap Analysis: Can underpass-runtime replace swe-ai-fleet/workspace?

> **Date**: 2026-03-09
> **Branch**: `main` (post Phase 1-6 + E2E)
> **Verdict**: **YES — underpass-runtime is a strict superset of swe-ai-fleet/workspace**

---

## 1. Executive Summary

underpass-runtime contains **every feature** present in swe-ai-fleet's workspace service,
plus 6 phases of additions (Docker runtime, event bus, discovery, portability, telemetry, learning loop).
The tool catalog is **identical** (99 capabilities, same YAML). All env vars used in swe-ai-fleet's
production deployment exist in underpass-runtime. The replacement is a **drop-in** at the
configuration level — no code changes needed in consumers.

| Dimension | swe-ai-fleet/workspace | underpass-runtime | Gap? |
|-----------|----------------------|-------------------|------|
| Tool catalog | 99 tools, 15+ families | 99 tools, identical | None |
| HTTP API | /healthz, /metrics, /v1/sessions, /v1/invocations | Same + /discovery, /recommendations | Superset |
| Workspace backends | local, kubernetes | local, docker, kubernetes | Superset |
| Session store | memory, valkey | memory, valkey | None |
| Invocation store | memory, valkey | memory, valkey | None |
| Artifact store | local FS only | local FS + S3/MinIO | Superset |
| Event bus | None | noop, NATS JetStream + outbox | Superset |
| Telemetry | OTEL only | OTEL + valkey recorder + aggregator | Superset |
| Discovery | Basic list | Compact/full + filters + recommendations | Superset |
| Portability | None | Snapshots + S3 rehydration | Superset |
| Policy engine | Static (identical) | Static (identical) | None |
| Auth modes | payload, trusted_headers | payload, trusted_headers | None |
| K8s build | Always compiled | Behind `//go:build k8s` tag | Better |
| Runner images | 6 profiles (base, toolchains, secops, container, k6, fat) | Same Dockerfile, same profiles | None |
| Bootstrap | Monolithic main.go (100+ handlers) | Modular bundle registry | Better |

---

## 2. Detailed Gap Analysis

### 2.1 ZERO GAPS (features identical or improved)

#### Tool Catalog — Identical
```
$ diff <(grep "^- name:" swe-ai-fleet/catalog) <(grep "^- name:" underpass-runtime/catalog)
(no differences)
```
Both: 99 capabilities across fs, git, repo, container, image, artifact, k8s, connection, api, ci,
go, rust, node, python, c, redis, mongo, nats, kafka, rabbit, security, sbom, quality.

#### HTTP API — Compatible Superset
swe-ai-fleet routes:
- `POST   /v1/sessions`
- `DELETE /v1/sessions/{id}`
- `GET    /v1/sessions/{id}/tools`
- `POST   /v1/sessions/{id}/tools/{name}/invoke`
- `GET    /v1/invocations/{id}`
- `GET    /v1/invocations/{id}/logs`
- `GET    /v1/invocations/{id}/artifacts`
- `GET    /healthz`
- `GET    /metrics`

underpass-runtime adds:
- `GET    /v1/sessions/{id}/tools/discovery?detail=compact|full`
- `GET    /v1/sessions/{id}/tools/recommendations?task_hint=...&top_k=10`

**No breaking changes.** All existing API consumers work unchanged.

#### Env Vars — Full Parity
Every env var from swe-ai-fleet's production deployment exists in underpass-runtime:

| Env Var | swe-ai-fleet | underpass-runtime |
|---------|-------------|-------------------|
| `WORKSPACE_BACKEND` | local, kubernetes | local, docker, kubernetes |
| `WORKSPACE_K8S_*` (18 vars) | All | All |
| `WORKSPACE_AUTH_*` (5 vars) | All | All |
| `WORKSPACE_CONN_PROFILE_*` (2 vars) | All | All |
| `WORKSPACE_CONTAINER_STRICT_BY_DEFAULT` | Yes | Yes |
| `WORKSPACE_CONTAINER_ALLOW_SYNTHETIC_FALLBACK` | Yes | Yes |
| `WORKSPACE_ENABLE_K8S_DELIVERY_TOOLS` | Yes | Yes |
| `WORKSPACE_RATE_LIMIT_PER_MINUTE` | Yes | Yes |
| `WORKSPACE_MAX_CONCURRENCY_PER_SESSION` | Yes | Yes |
| `WORKSPACE_OTEL_*` (4 vars) | All | All |
| `VALKEY_*` (4 vars) | All | All |
| `SESSION_STORE_*` | All | All |
| `INVOCATION_STORE_*` | All | All |

New in underpass-runtime (unused by swe-ai-fleet, safe defaults):
- `WORKSPACE_DOCKER_*` (8 vars) — only active if `WORKSPACE_BACKEND=docker`
- `ARTIFACT_BACKEND`, `ARTIFACT_S3_*` — defaults to `local` (same as swe-ai-fleet)
- `EVENT_BUS`, `EVENT_BUS_NATS_*` — defaults to `none` (same as swe-ai-fleet)
- `TELEMETRY_BACKEND`, `TELEMETRY_*` — defaults to `none`
- `WORKSPACE_DISABLED_BUNDLES` — empty by default (all bundles enabled)

#### Policy Engine — Identical
Same `StaticPolicy` with all 12 validation stages (scope, risk, approval, paths, args,
profiles, subjects, topics, queues, key prefixes, namespaces, registries).

#### K8s Runtime — Identical Features, Better Build
swe-ai-fleet compiles K8s client in every build. underpass-runtime gates it behind
`//go:build k8s`. For K8s deployment, build with `-tags k8s` — same features,
same pod creation, same janitor, same security context, same image bundles.

### 2.2 MINOR GAPS — Configuration/Deployment Only

#### Gap 1: PrometheusRule Template Missing from Helm Chart
**Severity**: Low
**Detail**: swe-ai-fleet has `workspace-prometheusrule.yaml` with alerts (WorkspaceDown,
InvocationFailureRateHigh). underpass-runtime Helm chart has ServiceMonitor but no
PrometheusRule template.
**Fix**: Add `prometheusrule.yaml` template to Helm chart. ~30 lines.

#### Gap 2: Delivery ServiceAccount Not in Helm Chart
**Severity**: Low
**Detail**: swe-ai-fleet has a separate `workspace-delivery` ServiceAccount + Role + RoleBinding
for K8s delivery tools (apply manifests, rollout). underpass-runtime RBAC template only creates
the runtime SA.
**Fix**: Add optional delivery RBAC to Helm chart (gated by `deliveryTools.enabled`). ~40 lines.

#### Gap 3: Runner Images Not Published to underpass-runtime Registry
**Severity**: Medium
**Detail**: swe-ai-fleet's deployment references `registry.underpassai.com/swe-ai-fleet/workspace-runner-*:v0.1.0`
(6 profiles). underpass-runtime has the same `runner-images/Dockerfile` but images are not yet
built/pushed under the underpass-runtime registry path.
**Fix**: Build & push runner images as `registry.underpassai.com/underpass-runtime/workspace-runner-*:v1.0.0`.
Add runner image build target to CI or Makefile.

#### Gap 4: Residual `swe-ai-fleet-codebase-delete-me-when-standalone-extraction-finish/`
**Severity**: Low (cosmetic)
**Detail**: Untracked directory with old swe-ai-fleet code. Not in git, not in Docker image.
**Fix**: `rm -rf swe-ai-fleet-codebase-delete-me-when-standalone-extraction-finish/`

### 2.3 NO GAPS — Integration Points

#### No Fleet-Specific Integrations
swe-ai-fleet's workspace has **zero imports** from other fleet services (ceremony, planning,
fleet-proxy, orchestrator). Communication is purely via:
1. HTTP API (consumed by any client)
2. NATS events (optional, for async integration)
3. Valkey (shared state store)

The workspace is already **fully decoupled** — it doesn't know it's part of a fleet.
underpass-runtime can be dropped in as a replacement without any fleet-side code changes.

#### Auth Mode Compatible
swe-ai-fleet uses `WORKSPACE_AUTH_MODE=trusted_headers` with a shared token from a K8s secret.
underpass-runtime supports the same auth mode with the same header names and token mechanism.

---

## 3. Replacement Plan (Phase 7 Backlog)

### WS-P7-001: PrometheusRule Helm Template
| Field | Value |
|-------|-------|
| **Objective** | Add PrometheusRule template with workspace alerts |
| **Files** | `charts/underpass-runtime/templates/prometheusrule.yaml`, `values.yaml` |
| **DoD** | Alerts: WorkspaceDown, InvocationFailureRateHigh, PolicyDenialRateHigh. Gated by `prometheusRule.enabled` |
| **Effort** | Small (~1h) |
| **Depends on** | None |

### WS-P7-002: Delivery RBAC in Helm Chart
| Field | Value |
|-------|-------|
| **Objective** | Add optional workspace-delivery ServiceAccount + Role + RoleBinding |
| **Files** | `charts/underpass-runtime/templates/rbac.yaml`, `values.yaml` |
| **DoD** | `deliveryTools.enabled=true` creates delivery SA with deploy/service/configmap verbs |
| **Effort** | Small (~1h) |
| **Depends on** | None |

### WS-P7-003: Runner Image CI Pipeline
| Field | Value |
|-------|-------|
| **Objective** | Build & push 6 runner image profiles to underpass-runtime registry |
| **Files** | `runner-images/Makefile`, `.github/workflows/ci.yml` (or separate workflow) |
| **DoD** | `make runner-images-build` builds all 6 profiles. CI pushes to `registry.underpassai.com/underpass-runtime/workspace-runner-*` on tag |
| **Effort** | Medium (~2h) |
| **Depends on** | None |

### WS-P7-004: Clean Up Residual swe-ai-fleet Directory
| Field | Value |
|-------|-------|
| **Objective** | Remove `swe-ai-fleet-codebase-delete-me-when-standalone-extraction-finish/` |
| **Files** | Delete directory |
| **DoD** | Directory removed, `.gitignore` updated if needed |
| **Effort** | Trivial |
| **Depends on** | None |

### WS-P7-005: Integration Shim for swe-ai-fleet
| Field | Value |
|-------|-------|
| **Objective** | Replace swe-ai-fleet's workspace deployment with underpass-runtime Helm chart |
| **Files** | swe-ai-fleet: `deploy/k8s/30-microservices/workspace*.yaml` → Helm release, `docker-compose.yml` → new image ref |
| **DoD** | swe-ai-fleet's E2E tests pass with underpass-runtime image. Same API, same auth, same K8s RBAC |
| **Effort** | Medium (~3h) — coordinated across both repos |
| **Depends on** | WS-P7-003 (runner images), WS-P7-001 (alerts), WS-P7-002 (delivery RBAC) |

### WS-P7-006: Deprecate swe-ai-fleet/services/workspace
| Field | Value |
|-------|-------|
| **Objective** | Mark swe-ai-fleet workspace as deprecated, point to underpass-runtime |
| **Files** | swe-ai-fleet: `services/workspace/README.md`, root docs |
| **DoD** | README states "DEPRECATED: Use underpass-runtime". CI skips workspace tests if using external chart |
| **Effort** | Small (~30min) |
| **Depends on** | WS-P7-005 |

---

## 4. Migration Checklist for swe-ai-fleet

```
[ ] Build underpass-runtime with -tags k8s
[ ] Push to registry.underpassai.com/underpass-runtime/workspace:v1.0.0
[ ] Build & push runner images (6 profiles)
[ ] Deploy underpass-runtime Helm chart in swe-ai-fleet namespace
    - Set WORKSPACE_K8S_NAMESPACE=swe-ai-fleet
    - Set runner image refs to underpass-runtime registry
    - Set WORKSPACE_AUTH_MODE=trusted_headers
    - Set Valkey/NATS config matching fleet's infra
    - Enable EVENT_BUS=nats (optional — enables event-driven integration)
    - Enable TELEMETRY_BACKEND=valkey (optional — enables learning loop)
[ ] Run swe-ai-fleet E2E tests against new deployment
[ ] Remove old workspace deployment manifests from swe-ai-fleet
[ ] Add PrometheusRule for alerting parity
```

---

## 5. Forward Roadmap

### Phase 8 — LLM Integration Testing
> Test underpass-runtime in Docker mode with real LLMs (large and workspace-local vLLM models).
> Validate the full loop: LLM → tool discovery → invoke → artifacts → telemetry.

### Phase 9 — Public Release
> Prepare repository for open-source publication.
> Licensing, documentation, CI hardening, public registry images.

### Phase 10 — Tool Learning Service (DuckDB)

New Python microservice (`services/tool-learning/`) — bounded context for offline/nearline
tool-selection policy learning. DuckDB as analytical engine over MinIO Parquet lake.

#### 10.1 Architecture

- **Hexagonal**: domain → application (use cases + ports) → adapters
- **Stateless DuckDB** (Option A): no persistent DB file, each CronJob scans Parquet and
  writes results back. Avoids single-writer locking, simplifies HA.
- **K8s CronJob**: hourly (warm) + daily (cold) schedules

#### 10.2 Data Flow

```
Phase 6 Telemetry (Valkey)
    │
    ▼  TL-006: exporter
MinIO Telemetry Lake (Parquet, partitioned by dt/hour)
    │
    ▼  TL-003: DuckDB httpfs/S3 scan
DuckDB (stateless, in-memory)
    │  aggregate + Thompson Sampling
    ▼
┌───────────────┬────────────────┬──────────────────┐
│ MinIO (audit) │ Valkey (hot)   │ NATS event       │
│ policy/*.json │ tool_policy:*  │ ToolPolicyUpdated │
└───────────────┴────────────────┴──────────────────┘
```

#### 10.3 Storage Isolation (MinIO)

**Two logical domains — never cross-read:**

| Domain | Bucket | Access | Purpose |
|--------|--------|--------|---------|
| Workspace Store | `workspace-artifacts` | workspace-svc R/W | Execution artefacts (repos, builds, outputs, caches) |
| Telemetry Lake | `telemetry-lake` | telemetry-svc W, tool-learning R | Parquet partitions for learning |
| Policy Output | `telemetry-policy` | tool-learning R/W | Policy snapshots (audit trail) |

**Isolation strategy** (start Opción B, path to A):
- **Opción B (initial)**: 1 MinIO tenant, 3 buckets, 3 S3 users with scoped policies
  - `workspace-svc`: only `workspace-artifacts`
  - `telemetry-svc`: only write `telemetry-lake`
  - `tool-learning`: read `telemetry-lake`, R/W `telemetry-policy`
- **Opción A (target)**: 2 MinIO tenants (`minio-workspace`, `minio-telemetry`) with separate
  credentials, NetworkPolicies, endpoints. Migration path: change S3 endpoint env vars.

**Critical rule**: tool-learning **NEVER reads from workspace-store**. If workspace context
is needed (language, repo size, task type), it must be emitted as telemetry fields or
`TaskMetadata` events into the lake.

#### 10.4 Data Privacy & Key Model

**Pseudonymization in the lake:**
- `subject_id_hash = HMAC(tenant_key, user_id)` — group by user without storing PII
- `workspace_id_hash` — correlate without exposing workspace paths
- No paths, repo names, tokens, env vars in telemetry records

**Valkey key model (runtime consumption):**
```
valkey:tool_policy:{context_sig}:{tool_id} → {
  alpha, beta, p95_latency_ms, p95_cost,
  error_rate, n_samples, freshness_ts
}
```
`context_sig` uses categorical dimensions only: `task_family`, `lang`, `constraints_class`.
No sensitive workspace data in the key.

#### 10.5 Retention & Lifecycle

| Tier | Data | Retention | Action |
|------|------|-----------|--------|
| Hot | Raw hourly Parquet | 7–14 days | Lifecycle delete |
| Warm | Daily aggregates | 30–90 days | Compaction job rewrites partitions |
| Cold | Policy snapshots | 90+ days | Audit archive |
| Workspace caches | Build caches, temp outputs | Days | Session cleanup cron |
| Workspace outputs | Release artefacts | Months | Configurable per tenant |

Lifecycle rules configured in Helm values per bucket.

#### 10.6 Network Security

```
┌─────────────────┐     ┌──────────────────┐
│ workspace pods   │────▶│ minio-workspace  │
│ (SA: workspace)  │     │ bucket only      │
└─────────────────┘     └──────────────────┘

┌─────────────────┐     ┌──────────────────┐
│ tool-learning    │────▶│ minio-telemetry  │
│ (SA: learning)   │     │ lake + policy    │
└─────────────────┘     └──────────────────┘
```
- NetworkPolicies: workspace pods → minio-workspace only; tool-learning → minio-telemetry only
- Separate ServiceAccounts + separate S3 credentials (never share access keys)
- mTLS / service mesh: distinct identities per tenant

#### 10.7 Domain Model

```python
ToolInvocation(invocation_id, dt, ts, tool_id, agent_id, task_id,
               context_signature, outcome, error_type, latency_ms,
               cost_units, tool_version)

ToolPolicy(context_signature, tool_id, alpha, beta,
           p95_latency_ms, p95_cost, error_rate,
           n_samples, freshness_ts, confidence)

ContextSignature(task_family, lang, constraints_class)
```

#### 10.8 Analytical Schema (DuckDB over Parquet)

```sql
-- Fact table (partitioned Parquet in MinIO)
CREATE TABLE tool_invocations (
  invocation_id   VARCHAR,
  dt              DATE,
  ts              TIMESTAMP,
  tool_id         VARCHAR,
  agent_id_hash   VARCHAR,
  task_id         VARCHAR,
  context_signature VARCHAR,
  outcome         VARCHAR,    -- 'success' | 'failure'
  error_type      VARCHAR,
  latency_ms      BIGINT,
  cost_units      DOUBLE,
  tool_version    VARCHAR
);

-- Materialized aggregate (computed by CronJob)
SELECT
  dt, context_signature, tool_id,
  COUNT(*)                                             AS n,
  AVG(CASE WHEN outcome='success' THEN 1 ELSE 0 END)  AS p_success,
  QUANTILE_CONT(latency_ms, 0.95)                     AS p95_latency_ms,
  QUANTILE_CONT(cost_units, 0.95)                      AS p95_cost,
  AVG(CASE WHEN outcome='failure' THEN 1 ELSE 0 END)  AS error_rate
FROM tool_invocations
GROUP BY dt, context_signature, tool_id;
```

#### 10.9 Learning Algorithm

**Thompson Sampling with Beta priors** per `(context_signature, tool_id)`:
- `alpha = successes + prior_alpha` (prior_alpha=1)
- `beta = failures + prior_beta` (prior_beta=1)
- Runtime samples `Beta(alpha, beta)` to rank tools → automatic exploration

**Hard constraints** (filter before sampling):
- `p95_latency_ms > MAX_P95_LATENCY_MS` → exclude
- `error_rate > MAX_ERROR_RATE` → exclude
- `p95_cost > MAX_P95_COST` → exclude

**Poisoned metrics protection:**
- Cap latency at configurable max (discard outliers)
- Ignore cancelled invocations
- Separate infra failures from tool semantic failures

#### 10.10 Event Integration (NATS)

**Inputs** (consumed from NATS or exported from Valkey telemetry):
- `workspace.invocation.completed` — tool outcome + metrics
- `workspace.invocation.denied` — policy denial signal

**Outputs** (published to NATS):
- `tool_learning.policy.updated` — per context cluster or global
- `tool_learning.tool.degraded` — SLO violation alert

#### 10.11 Hexagonal Boundaries

| Layer | Components |
|-------|------------|
| **Domain** | ToolInvocation, ToolPolicy, ContextSignature, aggregation semantics, bandit model |
| **Application** | ComputeHourlyPolicyUseCase, ComputeDailyPolicyUseCase, PublishPolicyUseCase |
| **Ports** | TelemetryLakePort (read), PolicyStorePort (write), PolicyEventPublisherPort, ClockPort |
| **Adapters** | DuckDB+MinIO (lake), Valkey (policy), NATS (events) |

#### 10.12 Observability

CronJob metrics (emitted to stdout JSON or Prometheus pushgateway):
- `partitions_processed`, `rows_scanned`, `policy_keys_written`
- `time_to_policy_seconds`, `failures_by_stage` (read/transform/write/publish)

#### 10.13 Backlog

| ID | Task | Depends | Effort |
|----|------|---------|--------|
| TL-001 | Domain model + analytical schema (Parquet) | — | Small |
| TL-002 | Application layer (use cases + ports) | TL-001 | Small |
| TL-003 | DuckDB + MinIO adapter (lake reader) | TL-002 | Medium |
| TL-004 | Valkey adapter (policy store) | TL-002 | Small |
| TL-005 | NATS adapter (policy event publisher) | TL-002 | Small |
| TL-006 | Telemetry exporter (Phase 6 Valkey → Parquet in MinIO) | TL-001 | Medium |
| TL-007 | CronJob manifest + Dockerfile + Makefile | TL-003..005 | Medium |
| TL-008 | MinIO bucket isolation (3 users, 3 policies, NetworkPolicies) | — | Medium |
| TL-009 | Lifecycle rules (retention per tier in Helm values) | TL-008 | Small |
| TL-010 | E2E test (CronJob → policy computed → runtime reads priors) | TL-007 | Medium |

---

## 6. Conclusion

**underpass-runtime is a standalone, decoupled workspace runtime.** It shares no code
dependencies with swe-ai-fleet and can be adopted independently by any project that
needs governed tool execution for AI agents.

Key capabilities:
- Docker backend for local development without K8s
- Tool discovery with LLM-optimized compact view
- Task-aware tool recommendations with telemetry
- S3 artifact portability
- Workspace snapshots for session migration
- Domain events via NATS JetStream
- Transactional outbox for at-least-once event delivery
- 99 tools across 15+ families, all policy-governed
