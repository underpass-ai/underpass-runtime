# Tool Learning Service

Bayesian Thompson Sampling pipeline for tool selection ranking in the SWE AI Fleet.

## What it does

The tool-learning service reads telemetry invocations from a Parquet lake (MinIO/S3),
computes Thompson Sampling policies for every `(context_signature, tool_id)` pair,
and writes ranked policies to Valkey for real-time tool selection by workspace agents.

```
Telemetry Lake (S3/Parquet)
        |
        v
  DuckDB Aggregation    -- SQL: GROUP BY context, tool; P95, error_rate
        |
        v
  Thompson Sampling      -- Beta(successes+1, failures+1) priors
        |
        v
  Hard Constraints       -- max_p95_latency, max_error_rate, max_p95_cost
        |
        +---> Valkey     -- tool_policy:{ctx}:{tool} with TTL
        +---> S3 Audit   -- audit/dt=YYYY-MM-DD/hour=HH/snapshot-*.json
        +---> NATS Event -- tool_learning.policy.updated
```

## Quick start

### Zero-infrastructure demo

Everything runs in-memory (DuckDB + miniredis + embedded NATS):

```bash
make demo

# With constraints:
go run ./cmd/demo --hours=24 --per-hour=500 --max-error-rate=0.08 --max-p95-latency-ms=1000
```

### Run tests

```bash
make test               # Unit tests with race detector
make integration-test   # Full pipeline with embedded services
make coverage-core      # Coverage gate (80% on domain + app + adapters)
make lint               # golangci-lint + go vet
```

## Architecture

Hexagonal architecture with clean dependency inversion:

```
cmd/
  tool-learning/     Main binary (CronJob entry point)
  seed-lake/         Synthetic telemetry generator
  demo/              Self-contained demo (no infra needed)

internal/
  domain/            Pure domain: ToolPolicy, ThompsonSampler, PolicyConstraints
  app/               Use case: ComputePolicyUseCase, port interfaces
  adapters/
    duckdb/          TelemetryLakeReader — Parquet aggregation via DuckDB
    valkey/          PolicyStore — Redis-compatible policy persistence
    nats/            PolicyEventPublisher — update notifications
    s3/              PolicyAuditStore — snapshot trail
  integration/       Integration tests (build tag: integration)
```

## Thompson Sampling

The service uses a Beta-Binomial model with uniform priors:

- **Alpha** = successes + 1 (prior)
- **Beta** = failures + 1 (prior)
- **Confidence** = Alpha / (Alpha + Beta) = P(success | data)
- **Sample** = draw from Beta(Alpha, Beta) for stochastic ranking

This provides optimal explore/exploit trade-off: tools with limited data
get explored more, while high-success tools are exploited.

## Hard constraints

Tools violating **any** constraint are excluded (AND semantics):

| Flag | Description |
|------|-------------|
| `--max-p95-latency-ms` | Exclude tools with P95 latency above threshold |
| `--max-error-rate` | Exclude tools with error rate above threshold |
| `--max-p95-cost` | Exclude tools with P95 cost above threshold |

## NATS event contract

Subject: `tool_learning.policy.updated`

```json
{
  "event": "tool_learning.policy.updated",
  "ts": "2026-03-12T14:00:00Z",
  "schedule": "hourly",
  "policies_written": 14,
  "policies_filtered": 2
}
```

## Valkey key format

```
{prefix}:{context_signature}:{tool_id}
```

Example: `tool_policy:gen:go:std:fs.write`

Payload: JSON-serialized `ToolPolicy` with TTL (default 2h).

## Deployment

Deployed as a Kubernetes CronJob via Helm:

```yaml
toolLearning:
  enabled: true
  schedules:
    - name: hourly
      cron: "0 * * * *"
    - name: daily
      cron: "0 0 * * *"
  constraints:
    maxP95LatencyMs: 5000
    maxErrorRate: 0.15
```

See `charts/underpass-runtime/values.yaml` for full configuration.
