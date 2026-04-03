# Helm Installation Guide — underpass-runtime

## Overview

The runtime chart deploys the workspace execution service (gRPC). It connects
to the kernel's NATS and Valkey as a client. The cert-gen hook reads the
shared CA from the kernel and generates its own server + client certs.

**Deploy order**: kernel first, then runtime, then demo.

## Quick Start — Full mTLS with cert-gen

```bash
# Kernel must be deployed first (creates the shared CA)
helm upgrade --install underpass-runtime charts/underpass-runtime \
  -n underpass-runtime \
  --set image.tag=grpc-latest \
  --set image.pullPolicy=Always \
  --set certGen.enabled=true \
  --set config.logLevel=debug \
  -f charts/underpass-runtime/values.shared-infra.yaml \
  -f charts/underpass-runtime/values.mtls.example.yaml
```

This creates 4 secrets signed by `rehydration-kernel-internal-ca`:

| Secret | Purpose |
|--------|---------|
| `underpass-runtime-tls` | Runtime gRPC server cert |
| `underpass-runtime-nats-client-tls` | NATS client cert |
| `underpass-runtime-valkey-client-tls` | Valkey client cert |
| `underpass-runtime-s3-client-tls` | S3/MinIO client cert |

## Values Profiles

| File | Use Case |
|------|----------|
| `values.yaml` | Base defaults |
| `values.shared-infra.yaml` | Connect to kernel's NATS + Valkey |
| `values.mtls.example.yaml` | Full mTLS overlay (all transports) |
| `values.production.yaml` | Production: replicas, monitoring, network policies |
| `values.dev.yaml` | Local development (memory backends, no TLS) |

## Real Examples

### Development (no TLS, memory backends)

```bash
helm upgrade --install underpass-runtime charts/underpass-runtime \
  -n underpass-runtime --create-namespace \
  --set image.tag=grpc-latest \
  -f charts/underpass-runtime/values.dev.yaml
```

### Shared infra with full mTLS (current cluster state)

```bash
helm upgrade --install underpass-runtime charts/underpass-runtime \
  -n underpass-runtime \
  --set image.tag=grpc-latest \
  --set image.pullPolicy=Always \
  --set certGen.enabled=true \
  --set config.logLevel=debug \
  -f charts/underpass-runtime/values.shared-infra.yaml \
  -f charts/underpass-runtime/values.mtls.example.yaml
```

### Point to specific secrets (without cert-gen)

```bash
helm upgrade --install underpass-runtime charts/underpass-runtime \
  -n underpass-runtime \
  --set image.tag=grpc-latest \
  --set tls.existingSecret=underpass-runtime-tls \
  --set tls.mode=mutual \
  --set natsTls.existingSecret=underpass-runtime-nats-client-tls \
  --set natsTls.mode=mutual \
  --set natsTls.serverName=rehydration-kernel-nats \
  --set valkeyTls.existingSecret=underpass-runtime-valkey-client-tls \
  --set valkeyTls.enabled=true \
  --set valkeyTls.serverName=rehydration-kernel-valkey \
  -f charts/underpass-runtime/values.shared-infra.yaml
```

### Verify deployment

```bash
# Pod running with mTLS
kubectl get pods -n underpass-runtime -l app.kubernetes.io/name=underpass-runtime

# Logs show mTLS configured
kubectl logs -n underpass-runtime deployment/underpass-runtime --tail=10
# Expected: "Valkey TLS configured mode=mutual"
#           "NATS TLS configured mode=mutual"
#           "HTTP server TLS configured mode=mutual"

# Verify cert chain
kubectl get secret underpass-runtime-tls -n underpass-runtime \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | \
  openssl x509 -noout -subject -issuer
# Expected: subject=CN=underpass-runtime
#           issuer=CN=rehydration-kernel-internal-ca
```

### Run E2E tests

```bash
# Generate E2E client cert (one-time)
kubectl get secret rehydration-kernel-internal-ca -n underpass-runtime \
  -o jsonpath='{.data.tls\.crt}' | base64 -d > /tmp/ca.crt
kubectl get secret rehydration-kernel-internal-ca -n underpass-runtime \
  -o jsonpath='{.data.tls\.key}' | base64 -d > /tmp/ca.key
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:prime256v1 -out /tmp/e2e.key
openssl req -new -key /tmp/e2e.key -subj "/CN=e2e-test-client" | \
  openssl x509 -req -CA /tmp/ca.crt -CAkey /tmp/ca.key -CAcreateserial \
  -out /tmp/e2e.crt -days 365
kubectl create secret generic e2e-client-tls \
  --from-file=tls.crt=/tmp/e2e.crt --from-file=tls.key=/tmp/e2e.key \
  --from-file=ca.crt=/tmp/ca.crt -n underpass-runtime

# Run smoke tests
for test in 01-health 02-session-lifecycle 03-tool-discovery 05-invoke-basic; do
  job=$(grep "^  name:" "e2e/tests/$test/job.yaml" | head -1 | awk '{print $2}')
  kubectl delete job $job -n underpass-runtime 2>/dev/null
  kubectl apply -f "e2e/tests/$test/job.yaml"
done

# Wait and check results
kubectl wait --for=condition=complete \
  job/e2e-health job/e2e-session-lifecycle \
  job/e2e-tool-discovery job/e2e-invoke-basic \
  -n underpass-runtime --timeout=120s
```

### Certificate rotation

```bash
# Delete the cert to rotate
kubectl delete secret underpass-runtime-nats-client-tls -n underpass-runtime

# Upgrade regenerates only that one
helm upgrade underpass-runtime charts/underpass-runtime \
  -n underpass-runtime --reuse-values
```

## Service

| Service | Port | Protocol |
|---------|------|----------|
| `underpass-runtime` | 50053 | gRPC (mTLS) |
| (metrics) | 9090 | HTTP (Prometheus) |

## certGen Configuration

```yaml
certGen:
  enabled: false                    # Enable cert-gen hook Job
  image: ghcr.io/underpass-ai/underpass-runtime/cert-gen:v1.0.0
  caSecret: rehydration-kernel-internal-ca  # CA to read (from kernel)
  keyCurve: prime256v1
  validityDays: 365
```

## Connection to Kernel Infrastructure

The runtime is a **client** of the kernel's NATS and Valkey:

```
underpass-runtime
  ├── gRPC server (mTLS) ← clients connect here
  ├── → rehydration-kernel-nats:4222 (mTLS client)
  ├── → rehydration-kernel-valkey:6379 (mTLS client)
  └── → minio:9000 (mTLS client, optional)
```

Use `values.shared-infra.yaml` to point to the kernel's services. Use
`values.mtls.example.yaml` to enable mTLS on all transports.

## Observability Stack

A separate Helm chart deploys Grafana, Prometheus, Loki, OTEL Collector, and
the alert-relay webhook in a dedicated namespace. See
[underpass-ai/underpass-observability](https://github.com/underpass-ai/underpass-observability)
for installation instructions.

```bash
# Deploy observability stack (separate namespace)
helm install observability charts/observability-stack \
  -n observability --create-namespace \
  --set runtimeNamespace=underpass-runtime \
  --set grafanaIngress.enabled=true \
  --set grafanaIngress.host=grafana.example.com
```

The stack scrapes runtime metrics at `:9090` via cross-namespace ServiceMonitor
and receives OTLP traces from the runtime. Connect the runtime:

```bash
helm upgrade underpass-runtime charts/underpass-runtime \
  -n underpass-runtime --reuse-values \
  --set telemetry.otel.enabled=true \
  --set telemetry.otel.endpoint=http://otel-collector.observability.svc:4318 \
  --set telemetry.otel.insecure=true
```
