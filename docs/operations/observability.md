# Observability

This document describes the metrics, tracing, and telemetry architecture
of underpass-runtime.

---

## Metrics Architecture

underpass-runtime exposes Prometheus-compatible metrics at `/metrics`.
The canonical metric instruments now live in the OpenTelemetry SDK:

- OTLP metrics are pushed when `WORKSPACE_OTEL_ENABLED=true`.
- `/metrics` remains available as a transitional Prometheus text endpoint.
- The Prometheus payload is rendered from the OTel SDK state, not from
  hand-written metric structs.

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
            ├── OTelQualityObserver → OTel counters + histograms
            ├── SlogQualityObserver → structured JSON logs (Loki)
            ├── CompositeQualityObserver → fan-out to multiple backends
            └── NoopQualityObserver → tests / disabled
```

**Port**: `QualityObserver` interface in `internal/app/types.go`
**Value object**: `InvocationQualityMetrics` in `internal/domain/quality_metrics.go`
**Adapters**: `internal/adapters/telemetry/quality_observer.go`

### Invocation Metrics (domain-layer)

All metrics originate from the domain `Invocation` value object. When an
invocation completes, the `Service` records OTel instruments and separately
fans out the derived quality value object through the `QualityObserver` port:

```
Invocation completes
    │
    ├── invocationMetrics.Observe(invocation)
    │       ├── OTel meter instruments
    │       ├── invocations_total{tool, status}
    │       ├── denied_total{tool, reason}
    │       └── duration_ms{tool} (histogram)
    │
    ├── domain.ComputeInvocationQuality(invocation)
    │
    ├── CompositeQualityObserver.ObserveInvocationQuality(...)
    │       ├── OTelQualityObserver
    │       └── SlogQualityObserver
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

The `/metrics` endpoint is kept for compatibility with the existing
`ServiceMonitor`, but it is now a view over OTel metric data. OTLP is the
source of truth for metric production.

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
| `workspace_success_on_first_tool_rate` | gauge | Ratio of sessions where the first invocation succeeded |
| `workspace_success_on_first_tool_total` | counter | Number of first-tool outcomes recorded |
| `workspace_recommendation_acceptance_rate` | ratio | Recommended tool was actually used |
| `workspace_recommendation_total` | counter | Number of recommendation decisions recorded |
| `workspace_policy_denial_rate_bad_recommendation` | ratio | Denials after recommendation |
| `workspace_context_bytes_saved` | counter | Bytes saved by compact discovery |
| `workspace_sessions_created_total` | counter | Sessions successfully created |
| `workspace_sessions_closed_total` | counter | Sessions successfully closed |
| `workspace_discovery_requests_total` | counter | Discovery endpoint served |
| `workspace_invocations_denied_total` | counter | Denied invocations by reason |

### Quality Observer Metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `workspace_invocation_quality_total` | counter | `tool`, `status`, `latency_bucket`, `has_error`, `error_code` | Quality observations emitted by `OTelQualityObserver` |
| `workspace_invocation_quality_duration_ms` | histogram | `tool`, `status`, `latency_bucket`, `has_error`, `error_code` | Invocation quality duration histogram with OTel exemplars when spans are sampled |

---

## OpenTelemetry

The runtime always records metrics through the OTel SDK. When
`WORKSPACE_OTEL_ENABLED=true`, it exports both OTLP traces and OTLP metrics.

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
| `service.name` | `workspace` |
| `service.version` | `WORKSPACE_VERSION` env var |
| `deployment.environment` | `WORKSPACE_ENV` env var |

### Configuration

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_OTEL_ENABLED` | `false` | Enable OTLP trace and OTLP metric export |
| `WORKSPACE_OTEL_EXPORTER_OTLP_ENDPOINT` | SDK default | OTLP HTTP endpoint |
| `WORKSPACE_OTEL_EXPORTER_OTLP_INSECURE` | `false` | Disable TLS for OTLP |
| `WORKSPACE_OTEL_TLS_CA_PATH` | none | CA cert for OTLP TLS |
| `WORKSPACE_OTEL_METRIC_EXPORT_INTERVAL_SECONDS` | `15` | OTLP metric push interval for the periodic reader |

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

---

## Trace-to-Log Correlation

`TraceLogHandler` (`internal/tlsutil/trace_log_handler.go`) wraps slog and injects
`trace_id` and `span_id` from the OpenTelemetry span context into every structured
log record. This enables Grafana's derived fields to link logs to traces:

```json
{"level":"INFO","msg":"audit.tool_invocation","trace_id":"0af765...","span_id":"00f067...","tool":"fs.read_file"}
```

Enabled automatically — no configuration needed. Works with any OTLP collector.

---

## Metrics Port

Prometheus metrics are served on `:9090` (configurable via `METRICS_PORT`).
The port is exposed in both the Kubernetes Deployment and Service resources,
enabling ServiceMonitor scraping from external namespaces during the migration
to collector-driven metric ingestion.

---

## Observability Stack

A complete observability stack is available in a separate repository:
[underpass-ai/underpass-observability](https://github.com/underpass-ai/underpass-observability)

Components: Prometheus, Grafana (with pre-built dashboard), Loki, Promtail,
OpenTelemetry Collector, and alert-relay (Grafana webhook → NATS domain events).

Deploys in a dedicated namespace (e.g., `observability`) and monitors
underpass-runtime via cross-namespace ServiceMonitor.

---

## Alert → Agent Remediation

When Grafana fires an alert, the flow is:

```
Grafana AlertManager → alert-relay webhook → NATS observability.alert.firing
    → remediation-agent → CreateSession → InvokeTool → AcceptRecommendation → CloseSession
```

The `remediation-agent` (`cmd/remediation-agent/`) subscribes to NATS alert events
and runs playbooks against the runtime. See README.md for playbook details.
