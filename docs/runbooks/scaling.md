# Runbook: Scaling

## Horizontal Pod Autoscaler (HPA)

### Enable

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=2 \
  --set autoscaling.maxReplicas=10 \
  --set autoscaling.targetCPUUtilizationPercentage=70
```

### Verify

```bash
kubectl -n underpass-runtime get hpa underpass-runtime
kubectl -n underpass-runtime describe hpa underpass-runtime
```

### Prerequisites

- Metrics Server must be installed (`kubectl top pods` must work).
- Resource requests must be set (they are by default: 250m CPU, 256Mi RAM).

---

## Vertical Scaling

Adjust resource requests/limits for higher throughput per replica:

```yaml
# values-high-throughput.yaml
resources:
  requests:
    cpu: "1"
    memory: 512Mi
  limits:
    cpu: "4"
    memory: 2Gi
```

```bash
helm upgrade underpass-runtime charts/underpass-runtime \
  -n underpass-runtime \
  -f values-high-throughput.yaml
```

---

## Workspace Pod Scaling (K8s Backend)

When using `config.workspaceBackend=kubernetes`, each agent session creates a
runner pod. Scaling considerations:

| Dimension | Default | Tunable |
|---|---|---|
| Max concurrent sessions | No hard limit (bounded by cluster capacity) | Node pool size, resource quotas |
| Pod ready timeout | 120s | `kubernetesBackend.readyTimeoutSeconds` |
| Pod janitor interval | 60s | `kubernetesBackend.janitor.intervalSeconds` |
| Terminal pod TTL | 300s | `kubernetesBackend.janitor.terminalTTLSeconds` |

### Warning Signs

- Pods stuck in `Pending`: insufficient node capacity.
- Pods timing out on ready: slow image pulls (pre-pull runner images).
- High pod churn: reduce janitor aggressiveness or increase terminal TTL.

### Pre-pulling Runner Images

Create a DaemonSet to pre-pull runner images on all nodes:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: runner-image-prepull
spec:
  selector:
    matchLabels:
      app: runner-prepull
  template:
    metadata:
      labels:
        app: runner-prepull
    spec:
      initContainers:
        - name: pull-base
          image: ghcr.io/underpass-ai/underpass-runtime/runner:v1.0.0-base
          command: ["true"]
        - name: pull-toolchains
          image: ghcr.io/underpass-ai/underpass-runtime/runner:v1.0.0-toolchains
          command: ["true"]
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
```

---

## Infrastructure Scaling

### Valkey

For high session/invocation volume:

```bash
# Check memory usage
kubectl -n underpass-runtime exec deploy/valkey-master -- valkey-cli info memory

# Check connected clients
kubectl -n underpass-runtime exec deploy/valkey-master -- valkey-cli info clients
```

Scale by increasing Valkey memory limits or switching to Valkey Cluster.

### NATS

For high event throughput:

```bash
# Check JetStream status
kubectl -n underpass-runtime exec deploy/nats-0 -- nats server info
```

Scale by adding NATS cluster replicas.

---

## Capacity Planning

| Concurrent Sessions | Runtime Replicas | Valkey Memory | NATS |
|---|---|---|---|
| 10 | 1 | 128Mi | Single node |
| 50 | 2-3 | 256Mi | Single node |
| 200 | 5-8 | 1Gi | 3-node cluster |
| 500+ | 10+ (HPA) | 2Gi+ (Cluster) | 3-node cluster |

These are estimates. Actual requirements depend on tool invocation patterns,
session duration, and telemetry retention.
