# Kubernetes Deployment Guide

Step-by-step guide for deploying underpass-runtime to a Kubernetes cluster.

---

## Prerequisites

| Requirement | Minimum Version | Check Command |
|---|---|---|
| Kubernetes cluster | 1.28+ | `kubectl version` |
| Helm | 3.12+ | `helm version` |
| kubectl | 1.28+ | `kubectl version --client` |
| ghcr.io pull secret | — | `kubectl get secret ghcr-pull` |
| Namespace | — | `kubectl get ns underpass-runtime` |

### Create Namespace and Pull Secret

```bash
kubectl create namespace underpass-runtime

kubectl create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username=<github-user> \
  --docker-password=<github-pat> \
  -n underpass-runtime
```

---

## Minimal Deployment (Memory Backends)

No external dependencies. Sessions and invocations stored in-memory.

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  --set config.workspaceBackend=local
```

Verify:

```bash
kubectl -n underpass-runtime rollout status deployment/underpass-runtime
kubectl -n underpass-runtime port-forward svc/underpass-runtime 50053:50053
curl http://localhost:50053/healthz
```

---

## Production Deployment (Valkey + NATS + S3)

### 1. Deploy Infrastructure

Deploy Valkey, NATS, and MinIO in the same namespace or reference existing
instances.

```bash
# Valkey
helm repo add bitnami https://charts.bitnami.com/bitnami
helm install valkey bitnami/valkey -n underpass-runtime \
  --set auth.password=<valkey-password>

# Create Valkey password secret
kubectl create secret generic valkey-password \
  --from-literal=password=<valkey-password> \
  -n underpass-runtime

# NATS
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm install nats nats/nats -n underpass-runtime \
  --set config.jetstream.enabled=true

# MinIO (or use existing S3)
helm repo add minio https://charts.min.io/
helm install minio minio/minio -n underpass-runtime \
  --set rootUser=minioadmin \
  --set rootPassword=minioadmin
```

### 2. Create Secrets

```bash
# MinIO credentials for workspace service
kubectl create secret generic minio-workspace-svc \
  --from-literal=accessKey=workspace-svc \
  --from-literal=secretKey=<generated-key> \
  -n underpass-runtime

# MinIO credentials for tool-learning
kubectl create secret generic minio-tool-learning \
  --from-literal=accessKey=tool-learning \
  --from-literal=secretKey=<generated-key> \
  -n underpass-runtime

# Auth token for trusted_headers mode
kubectl create secret generic workspace-auth \
  --from-literal=sharedToken=<generated-token> \
  -n underpass-runtime
```

### 3. Deploy Runtime

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  -f charts/underpass-runtime/values.yaml \
  --set config.workspaceBackend=local \
  --set stores.backend=valkey \
  --set valkey.enabled=true \
  --set valkey.host=valkey-master \
  --set valkey.existingSecret=valkey-password \
  --set eventBus.type=nats \
  --set eventBus.nats.url=nats://nats:4222 \
  --set artifacts.backend=s3 \
  --set artifacts.s3.endpoint=minio:9000 \
  --set artifacts.s3.existingSecret=minio-workspace-svc \
  --set auth.mode=trusted_headers \
  --set auth.existingSecret=workspace-auth \
  --set minioIAM.enabled=true \
  --set minioIAM.endpoint=http://minio:9000
```

### 4. Verify

```bash
# Check deployment
kubectl -n underpass-runtime rollout status deployment/underpass-runtime

# Check logs
kubectl -n underpass-runtime logs -l app.kubernetes.io/name=underpass-runtime

# Helm test
helm test underpass-runtime -n underpass-runtime

# Smoke test
./e2e/run-e2e-tests.sh --tier smoke --namespace underpass-runtime
```

---

## Kubernetes Workspace Backend

To run workspace sessions as isolated pods (instead of local filesystem):

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  --set config.workspaceBackend=kubernetes \
  --set kubernetesBackend.namespace=underpass-runtime \
  --set kubernetesBackend.runnerImage=ghcr.io/underpass-ai/underpass-runtime/runner:v1.0.0-base \
  --set kubernetesBackend.rbac.create=true
```

This creates:
- A `Role` allowing pod creation, exec, log reading, and secret access.
- A `RoleBinding` to the service account.
- Runner pods per session with security context (non-root, drop ALL caps).

### Runner Image Profiles

| Profile | Image Suffix | Contents |
|---|---|---|
| base | `-base` | bash, curl, git, jq, openssh-client |
| toolchains | `-toolchains` | Go 1.25, Python 3, Node.js, Rust |
| secops | `-secops` | Trivy, Syft |
| container | `-container` | Podman, Buildah, Skopeo |
| k6 | `-k6` | k6 load testing |
| fat | `-fat` | All of the above |

Select via session metadata: `"runner_profile": "toolchains"`.

## Enabling Delivery Tools

Rollout and manifest mutation tools require the Kubernetes backend plus delivery
RBAC bound to the runtime pod.

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  --set config.workspaceBackend=kubernetes \
  --set kubernetesBackend.namespace=underpass-runtime \
  --set kubernetesBackend.runnerImage=ghcr.io/underpass-ai/underpass-runtime/runner:v1.0.0-base \
  --set kubernetesBackend.rbac.create=true \
  --set kubernetesBackend.deliveryTools.enabled=true
```

That wiring sets `WORKSPACE_ENABLE_K8S_DELIVERY_TOOLS=true` and binds the
delivery `Role` to the runtime `ServiceAccount` in the release namespace.

For the rollout-specialist tools added in runtime, create sessions with:

- `tool_profile=runtime-rollout-narrow`
- `environment=<env>`
- `runtime_environment=<same-env>` if you want the session to carry the runtime environment explicitly

The job-based E2E test `23-runtime-rollout-tools` expects exactly this setup.

---

## Enabling Monitoring

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.release=kube-prometheus-stack \
  --set prometheusRule.enabled=true \
  --set prometheusRule.labels.release=kube-prometheus-stack
```

This creates:
- `ServiceMonitor` for Prometheus scraping (`/metrics`, 30s interval).
- `PrometheusRule` with 6 alerts (WorkspaceDown, FailureRateHigh,
  DeniedRateHigh, P95LatencyHigh, ToolLearningFailed, ToolLearningMissed).

---

## Enabling Network Policies

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -n underpass-runtime \
  --set networkPolicy.enabled=true
```

Restricts egress to: DNS (53), Valkey (6379), NATS (4222), S3/MinIO (443,
9000), K8s API (443, 6443), OTLP (4318).

---

## TLS Deployment

See [deployment-tls.md](deployment-tls.md) for full TLS configuration
across all five transports (HTTP, Valkey, NATS, S3, OTLP).
