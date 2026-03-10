# Tool Policy Contract v1

Shared contract between the **workspace** (runtime) service and the **tool-learning** service.
Both services implement their own readers/writers against this contract independently.
Neither imports code from the other.

## Valkey Key Format

```
tool_policy:{context_signature}:{tool_id}
```

**Example:**
```
tool_policy:code-gen:go:standard:fs.write
```

### Context Signature

Colon-separated categorical dimensions:

| Field | Description | Examples |
|-------|-------------|----------|
| `task_family` | High-level task category | `code-gen`, `test`, `review`, `refactor` |
| `lang` | Programming language | `go`, `python`, `rust`, `node` |
| `constraints_class` | Constraint profile | `standard`, `strict`, `permissive` |

Full format: `{task_family}:{lang}:{constraints_class}`

## Valkey Value Schema (JSON)

```json
{
  "context_signature": "code-gen:go:standard",
  "tool_id": "fs.write",
  "alpha": 91.0,
  "beta": 11.0,
  "p95_latency_ms": 250,
  "p95_cost": 0.5,
  "error_rate": 0.1,
  "n_samples": 100,
  "freshness_ts": "2026-03-09T12:00:00Z",
  "confidence": 0.892
}
```

### Field Descriptions

| Field | Type | Description |
|-------|------|-------------|
| `alpha` | float64 | Beta distribution alpha (successes + prior) |
| `beta` | float64 | Beta distribution beta (failures + prior) |
| `p95_latency_ms` | int64 | 95th percentile latency in milliseconds |
| `p95_cost` | float64 | 95th percentile cost in abstract units |
| `error_rate` | float64 | Failure rate [0.0, 1.0] |
| `n_samples` | int64 | Total invocations observed |
| `freshness_ts` | RFC3339 | When this policy was last computed |
| `confidence` | float64 | alpha / (alpha + beta) — point estimate |

## NATS Events

### `tool_learning.policy.updated`

Published by tool-learning after policy recomputation.
Consumed by workspace for cache invalidation or live ranking refresh.

```json
{
  "event": "tool_learning.policy.updated",
  "ts": "2026-03-09T12:00:00Z",
  "schedule": "hourly",
  "policies_written": 42,
  "policies_filtered": 3
}
```

### `tool_learning.tool.degraded`

Published when a tool violates SLO constraints.

```json
{
  "event": "tool_learning.tool.degraded",
  "ts": "2026-03-09T12:00:00Z",
  "tool_id": "api.benchmark",
  "context_signature": "test:go:standard",
  "p95_latency_ms": 12000,
  "error_rate": 0.35,
  "constraint_violated": "max_p95_latency_ms"
}
```

## Telemetry Lake (Parquet)

### Bucket: `telemetry-lake`

Partitioned by `dt` (date) and `hour`.

```
telemetry-lake/
  dt=2026-03-09/
    hour=12/
      invocations-001.parquet
```

### Schema

| Column | Type | Description |
|--------|------|-------------|
| `invocation_id` | VARCHAR | Unique invocation ID |
| `dt` | DATE | Partition date |
| `ts` | TIMESTAMP | Invocation timestamp |
| `tool_id` | VARCHAR | Tool name (e.g., `fs.write`) |
| `agent_id_hash` | VARCHAR | HMAC-pseudonymized agent ID |
| `task_id` | VARCHAR | Task identifier |
| `context_signature` | VARCHAR | `{task_family}:{lang}:{constraints_class}` |
| `outcome` | VARCHAR | `success` or `failure` |
| `error_type` | VARCHAR | Error classification (empty on success) |
| `latency_ms` | BIGINT | Execution duration in milliseconds |
| `cost_units` | DOUBLE | Abstract cost metric |
| `tool_version` | VARCHAR | Tool implementation version |

## Versioning

This contract is versioned. Breaking changes require a new version (`v2/`).
Both services must support the same contract version to interoperate.

## Writers and Readers

| Operation | Writer | Reader |
|-----------|--------|--------|
| Telemetry records → Valkey | Workspace | Telemetry exporter (tool-learning) |
| Parquet → MinIO lake | Telemetry exporter (tool-learning) | DuckDB aggregator (tool-learning) |
| Tool policies → Valkey | Tool-learning | Workspace (recommendations) |
| Policy events → NATS | Tool-learning | Workspace (cache refresh) |
