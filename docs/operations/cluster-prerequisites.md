# Cluster Prerequisites

Checklist of cluster-level requirements before deploying underpass-runtime.

---

## Required

| Component | Purpose | Verification |
|---|---|---|
| **Kubernetes 1.28+** | API compatibility | `kubectl version` |
| **Container runtime** | Pod execution | `kubectl get nodes -o wide` (containerd/CRI-O) |
| **CoreDNS** | Service discovery | `kubectl get pods -n kube-system -l k8s-app=kube-dns` |
| **Default StorageClass** | PVC for Valkey/MinIO (if deploying infra) | `kubectl get storageclass` |
| **ghcr.io access** | Pull runtime images | `kubectl create secret docker-registry ghcr-pull ...` |

## Optional (by feature)

| Feature | Requires | Verification |
|---|---|---|
| **TLS (cert-manager)** | cert-manager 1.12+ | `kubectl get pods -n cert-manager` |
| **Monitoring** | Prometheus Operator (kube-prometheus-stack) | `kubectl get crd prometheusrules.monitoring.coreos.com` |
| **Network policies** | CNI with NetworkPolicy support (Calico, Cilium) | `kubectl get networkpolicy -A` |
| **HPA** | Metrics Server | `kubectl top pods` |
| **GPU workloads (vLLM)** | NVIDIA GPU Operator + device plugin | `kubectl get nodes -o json \| jq '.items[].status.capacity["nvidia.com/gpu"]'` |
| **Ingress (vLLM)** | NGINX Ingress Controller | `kubectl get ingressclass` |

## Namespace Setup

```bash
# Create namespace
kubectl create namespace underpass-runtime

# Label for network policy selectors (if using)
kubectl label namespace underpass-runtime app.kubernetes.io/part-of=underpass

# Create image pull secret
kubectl create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username=<github-user> \
  --docker-password=<github-pat> \
  -n underpass-runtime
```

## Resource Estimates

| Component | CPU Request | Memory Request | Storage |
|---|---|---|---|
| underpass-runtime (1 replica) | 250m | 256Mi | 2Gi ephemeral (workspaces) |
| Valkey | 100m | 128Mi | 1Gi PVC |
| NATS (JetStream) | 100m | 128Mi | 1Gi PVC |
| MinIO | 250m | 512Mi | 10Gi PVC |
| Tool-learning CronJob | 500m (burst 2) | 512Mi (burst 1Gi) | — |

**Total baseline**: ~1.2 CPU, ~1.5 Gi RAM, ~14 Gi storage.

With Kubernetes workspace backend, add resources per concurrent session:
each runner pod requests ~250m CPU and ~256Mi RAM (depends on runner profile).
