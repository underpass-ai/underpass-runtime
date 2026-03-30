# Observability

This document describes the metrics, tracing, and telemetry architecture
of underpass-runtime.

---

## Metrics Architecture

underpass-runtime exposes Prometheus-compatible metrics at `/metrics`.
Metrics are computed in-process — no external metrics library dependency.

### Domain Value Object: InvocationQualityMetrics

Quality metrics are computed as a **domain value object** — invariant-validated
at construction time, immutable, and observed through a hexagonal port.
This mirrors rehydration-kernel's `BundleQualityMetrics` pattern.

```
Invocation completes
    │
    ├── domain.ComputeInvocationQuality(invocation)
    │       → InvocationQualityMetrics (value object)
    │           ├── tool_name, status, duration_ms, exit_code
    │           ├── latency_bucket (fast/normal/slow/very_slow)
    │           ├── success_rate (1.0 or 0.0)
    │           └── error_code (if failed/denied)
    │
    └── QualityObserver.ObserveInvocationQuality(metrics, context)
            ├── SlogQualityObserver → structured JSON logs (Loki)
            ├── CompositeQualityObserver → fan-out to multiple backends
            └── NoopQualityObserver → tests / disabled
```

**Port**: `QualityObserver` interface in `internal/app/types.go`
**Value object**: `InvocationQualityMetrics` in `internal/domain/quality_metrics.go`
**Adapters**: `internal/adapters/telemetry/quality_observer.go`

### Invocation Metrics (domain-layer)

All metrics originate from the domain `Invocation` value object. When an
invocation completes, the `Service` passes the full `domain.Invocation` to
the metrics observer:

```
Invocation completes
    │
    ├── invocationMetrics.Observe(invocation)
    │       ├── invocations_total{tool, status}
    │       ├── denied_total{tool, reason}
    │       └── duration_ms{tool} (histogram)
    │
    └── KPIMetrics.Observe*(...)
            ├── tool_calls_per_task{context}
            ├── first_tool_success_rate
            ├── recommendation_acceptance_rate
            ├── policy_denial_after_recommendation_rate
            ├── context_bytes_saved
            ├── sessions_created_total
            ├── sessions_closed_total
            ├── discovery_requests_total
            └── invocations_denied_total{reason}
```

### Prometheus Exposition

| Metric | Type | Labels | Description |
|---|---|---|---|
| `invocations_total` | counter | `tool`, `status` | Total tool invocations by tool name and final status (succeeded, failed, denied) |
| `denied_total` | counter | `tool`, `reason` | Denied invocations by tool and denial reason |
| `duration_ms` | histogram | `tool` | Invocation duration in milliseconds |

**Histogram buckets**: 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000 ms

### KPI Metrics (learning loop)

| Metric | Type | Description |
|---|---|---|
| `workspace_tool_calls_per_task` | counter | Tool invocations grouped by task context |
| `workspace_success_on_first_tool` | ratio | First invocation per session succeeded |
| `workspace_recommendation_acceptance_rate` | ratio | Recommended tool was actually used |
| `workspace_policy_denial_rate_bad_recommendation` | ratio | Denials after recommendation |
| `workspace_context_bytes_saved` | counter | Bytes saved by compact discovery |
| `workspace_sessions_created_total` | counter | Sessions successfully created |
| `workspace_sessions_closed_total` | counter | Sessions successfully closed |
| `workspace_discovery_requests_total` | counter | Discovery endpoint served |
| `workspace_invocations_denied_total` | counter | Denied invocations by reason |

---

## OpenTelemetry Tracing

When `WORKSPACE_OTEL_ENABLED=true`, the service exports OTLP traces.

### Trace Structure

```
workspace.service (root span)
├── workspace.session.create
├── workspace.session.close
├── workspace.tools.list
├── workspace.tools.discover
├── workspace.tools.recommend
└── workspace.invocation.invoke
    ├── workspace.policy.authorize
    ├── workspace.tools.<tool_name>   (tool execution span)
    ├── workspace.artifacts.save
    └── workspace.audit.record
```

### Resource Attributes

| Attribute | Source |
|---|---|
| `service.name` | `underpass-runtime` |
| `service.version` | `WORKSPACE_VERSION` env var |
| `deployment.environment` | `WORKSPACE_ENV` env var |

### Configuration

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_OTEL_ENABLED` | `false` | Enable OTLP trace exporter |
| `WORKSPACE_OTEL_EXPORTER_OTLP_ENDPOINT` | SDK default | OTLP HTTP endpoint |
| `WORKSPACE_OTEL_EXPORTER_OTLP_INSECURE` | `false` | Disable TLS for OTLP |
| `WORKSPACE_OTEL_TLS_CA_PATH` | none | CA cert for OTLP TLS |

---

## Internal Telemetry Store

The runtime has a built-in telemetry store for the tool-learning pipeline:

| Backend | Use Case | TTL |
|---|---|---|
| `none` | No telemetry recording (default) | — |
| `memory` | In-process, development/testing | — |
| `valkey` | Persistent, feeds tool-learning CronJob | 7 days default |

Telemetry records are aggregated every 5 minutes (configurable via
`TELEMETRY_AGGREGATION_INTERVAL_SECONDS`) and consumed by the tool-learning
pipeline to compute Thompson Sampling policies.

---

## Prometheus Alerts

When `prometheusRule.enabled=true` in Helm, the following alerts are active:

| Alert | Condition | Severity | For |
|---|---|---|---|
| `WorkspaceDown` | Pod unhealthy | critical | 5m |
| `WorkspaceInvocationFailureRateHigh` | >5% failure rate (with >=0.2 req/s) | warning | 10m |
| `WorkspaceInvocationDeniedRateHigh` | >0.5 denied req/s | warning | 10m |
| `WorkspaceP95InvocationLatencyHigh` | P95 >2000ms | warning | 10m |
| `ToolLearningCronJobFailed` | CronJob failed | warning | 0m |
| `ToolLearningCronJobMissed` | Hourly job missing >2h | warning | 0m |

See `charts/underpass-runtime/templates/prometheusrule.yaml` for PromQL expressions.

---

## Grafana Integration

For Grafana dashboards, use the following queries:

**Invocation rate by status:**
```promql
sum(rate(invocations_total[5m])) by (status)
```

**P95 latency by tool:**
```promql
histogram_quantile(0.95, sum(rate(duration_ms_bucket[5m])) by (tool, le))
```

**Denial rate:**
```promql
sum(rate(denied_total[5m])) by (reason)
```

**Tool success rate:**
```promql
sum(rate(invocations_total{status="succeeded"}[5m])) by (tool)
/
sum(rate(invocations_total[5m])) by (tool)
```
