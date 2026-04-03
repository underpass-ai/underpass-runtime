# Observability Stack Installation

Deploys Prometheus, Grafana, Loki, Promtail, and OpenTelemetry Collector
in a dedicated `observability` namespace. Monitors underpass-runtime
running in a separate namespace.

## Prerequisites

- Kubernetes cluster with Helm 3+
- underpass-runtime deployed (any namespace)

## Install

```bash
# Add chart repositories
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add grafana https://grafana.github.io/helm-charts
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm repo update

# Build dependencies
helm dependency build charts/observability-stack

# Install in dedicated namespace
helm install observability charts/observability-stack \
  -n observability --create-namespace \
  --set runtimeNamespace=underpass-runtime
```

## Connect underpass-runtime to the stack

Update the runtime Helm release to point OTEL traces to the collector:

```bash
helm upgrade underpass-runtime charts/underpass-runtime \
  -n underpass-runtime --reuse-values \
  --set telemetry.otel.enabled=true \
  --set telemetry.otel.endpoint=http://otel-collector.observability.svc:4318 \
  --set telemetry.otel.insecure=true
```

## Access

```bash
# Grafana (admin / underpass-admin)
kubectl port-forward -n observability svc/grafana 3000:80

# Prometheus
kubectl port-forward -n observability svc/prometheus-kube-prometheus-prometheus 9090:9090
```

## Architecture

```
underpass-runtime (namespace: underpass-runtime)
  ├── gRPC :50053     → serves agents
  ├── metrics :9090   → scraped by Prometheus (cross-namespace ServiceMonitor)
  ├── JSON logs       → collected by Promtail → pushed to Loki
  └── OTLP traces     → sent to OTEL Collector :4318
                              ↓
observability (namespace: observability)
  ├── Prometheus      ← scrapes runtime metrics
  ├── Grafana         ← dashboards (auto-provisioned)
  ├── Loki            ← receives logs from Promtail
  ├── Promtail        ← tails pod logs (DaemonSet)
  └── OTEL Collector  ← receives OTLP traces
```
