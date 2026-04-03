# Workspace Service -- Environment Variable Reference

Complete reference for all environment variables consumed by the workspace service
(`cmd/workspace/main.go` and `cmd/workspace/main_k8s.go`).

---

## Core

| Variable | Default | Description |
|---|---|---|
| `PORT` | `50053` | gRPC listen port. |
| `METRICS_PORT` | `9090` | Prometheus metrics HTTP port. Exposed at `/metrics`. |
| `LOG_LEVEL` | `info` | Log verbosity. Accepted values: `debug`, `info`, `warn`, `error` (case-insensitive). |
| `WORKSPACE_ROOT` | `/tmp/underpass-workspaces` | Filesystem root for local workspaces. Only used when `WORKSPACE_BACKEND=local`. |
| `ARTIFACT_ROOT` | `/tmp/underpass-artifacts` | Filesystem root for the local artifact store. Created automatically on startup. |
| `WORKSPACE_BACKEND` | `local` | Workspace lifecycle backend. Values: `local`, `docker`, `kubernetes`. The `kubernetes` variant requires the `k8s` build tag. |
| `WORKSPACE_DISABLED_BUNDLES` | _(empty -- all enabled)_ | Comma-separated list of tool bundle names to disable (e.g. `messaging,data`). |

---

## TLS -- HTTP Server

Controls TLS on the workspace HTTP server itself.

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_TLS_MODE` | `disabled` | TLS mode for the HTTP listener. Values: `disabled` / `plaintext` (plain HTTP), `server` / `tls` (server-side TLS), `mutual` / `mtls` (mutual TLS -- requires client CA). |
| `WORKSPACE_TLS_CERT_PATH` | _(none)_ | Path to the PEM-encoded server certificate. Required when mode is `server` or `mutual`. |
| `WORKSPACE_TLS_KEY_PATH` | _(none)_ | Path to the PEM-encoded server private key. Required when mode is `server` or `mutual`. |
| `WORKSPACE_TLS_CLIENT_CA_PATH` | _(none)_ | Path to a PEM CA bundle used to verify client certificates. Required when mode is `mutual`. |

---

## TLS -- Valkey

Controls TLS for all Valkey connections (session store, invocation store, telemetry, outbox).
The Go runtime uses explicit env vars (not the `rediss://` URI scheme used by the Rust kernel).

| Variable | Default | Description |
|---|---|---|
| `VALKEY_TLS_ENABLED` | `false` | Enable TLS for Valkey connections. Accepts `true`/`false`/`1`/`0`/`yes`/`no`. |
| `VALKEY_TLS_CA_PATH` | _(none)_ | Path to PEM CA bundle for verifying the Valkey server certificate. |
| `VALKEY_TLS_SERVER_NAME` | _(empty)_ | Optional SNI / hostname verification override for the Valkey server certificate. Use this when the service DNS name used for connection does not match the certificate SAN you want to verify. |
| `VALKEY_TLS_CERT_PATH` | _(none)_ | Path to PEM client certificate for mutual TLS. Optional -- when both cert and key are set, mode upgrades from `server` to `mutual`. |
| `VALKEY_TLS_KEY_PATH` | _(none)_ | Path to PEM client private key for mutual TLS. |

---

## TLS -- NATS

Controls TLS for the NATS event-bus connection.

| Variable | Default | Description |
|---|---|---|
| `NATS_TLS_MODE` | `disabled` | TLS mode for the NATS client. Values: `disabled` / `plaintext`, `server` / `tls`, `mutual` / `mtls`. |
| `NATS_TLS_CA_PATH` | _(none)_ | Path to PEM CA bundle for verifying the NATS server certificate. |
| `NATS_TLS_SERVER_NAME` | _(empty)_ | Optional SNI / hostname verification override for the NATS server certificate. Use this when the runtime connects through a service name or alias that differs from the certificate SAN to verify. |
| `NATS_TLS_CERT_PATH` | _(none)_ | Path to PEM client certificate (mutual TLS). |
| `NATS_TLS_KEY_PATH` | _(none)_ | Path to PEM client private key (mutual TLS). |
| `NATS_TLS_FIRST` | `false` | Env-var parity with the Rust kernel. The Go `nats.go` client does **not** support TLS-first handshake; if set to `true`, a warning is logged and the flag is ignored. |

---

## Session Store

| Variable | Default | Description |
|---|---|---|
| `SESSION_STORE_BACKEND` | `memory` | Session persistence backend. Values: `memory`, `valkey`. |
| `SESSION_STORE_KEY_PREFIX` | `workspace:session` | Redis/Valkey key prefix for session records. Only used when backend is `valkey`. |
| `SESSION_STORE_TTL_SECONDS` | `86400` | Session TTL in seconds (default 24 h). Only used when backend is `valkey`. |

---

## Invocation Store

| Variable | Default | Description |
|---|---|---|
| `INVOCATION_STORE_BACKEND` | `memory` | Invocation persistence backend. Values: `memory`, `valkey`. |
| `INVOCATION_STORE_KEY_PREFIX` | `workspace:invocation` | Redis/Valkey key prefix for invocation records. Only used when backend is `valkey`. |
| `INVOCATION_STORE_TTL_SECONDS` | `86400` | Invocation TTL in seconds (default 24 h). Only used when backend is `valkey`. |

---

## Decision Store

Persists recommendation decisions for the learning evidence plane. Durable storage ensures audit trail survives service restarts.

| Variable | Default | Description |
|---|---|---|
| `DECISION_STORE_BACKEND` | `memory` | Decision persistence backend. Values: `memory`, `valkey`. |
| `DECISION_STORE_KEY_PREFIX` | `workspace:decision` | Redis/Valkey key prefix for decision records. Only used when backend is `valkey`. |
| `DECISION_STORE_TTL_SECONDS` | `2592000` | Decision TTL in seconds (default 30 days). Only used when backend is `valkey`. |

---

## Valkey Connection

Shared by all Valkey-backed subsystems (session store, invocation store, decision store, telemetry, outbox).

| Variable | Default | Description |
|---|---|---|
| `VALKEY_ADDR` | _(none)_ | Full `host:port` address. When set, takes precedence over `VALKEY_HOST` + `VALKEY_PORT`. |
| `VALKEY_HOST` | `localhost` | Valkey hostname. Ignored when `VALKEY_ADDR` is set. |
| `VALKEY_PORT` | `6379` | Valkey port. Ignored when `VALKEY_ADDR` is set. |
| `VALKEY_PASSWORD` | _(empty)_ | Valkey AUTH password. |
| `VALKEY_DB` | `0` | Valkey database index (integer). |

---

## Learned Policy Reader

Activated automatically when `INVOCATION_STORE_BACKEND=valkey`. Reads
offline-computed policies from Valkey for online recommendation scoring.

| Variable | Default | Description |
|---|---|---|
| `POLICY_KEY_PREFIX` | `tool_policy` | Valkey key prefix for learned policies. Keys follow `{prefix}:{context_sig}:{tool_id}`. |

The neural model (NeuralTS) is loaded from Valkey key `neural_ts:model:v1`
using the same connection. No additional configuration needed.

---

## Event Bus

| Variable | Default | Description |
|---|---|---|
| `EVENT_BUS` | `none` | Event publisher backend. Values: `none` (noop), `nats`. |
| `EVENT_BUS_NATS_URL` | `nats://localhost:4222` | NATS server URL. Only used when `EVENT_BUS=nats`. |
| `EVENT_BUS_NATS_STREAM` | _(empty)_ | JetStream stream name. Empty string disables JetStream (uses core NATS publish). |
| `EVENT_BUS_OUTBOX` | `false` | Enable the Valkey-backed outbox relay between the service and the NATS publisher. Requires a working Valkey connection. |
| `EVENT_BUS_OUTBOX_KEY_PREFIX` | `workspace:outbox` | Redis/Valkey key prefix for outbox entries. |

---

## Artifact Store

| Variable | Default | Description |
|---|---|---|
| `ARTIFACT_BACKEND` | `local` | Artifact persistence backend. Values: `local`, `s3`. |
| `ARTIFACT_S3_BUCKET` | `workspace-artifacts` | S3 bucket name. |
| `ARTIFACT_S3_PREFIX` | _(empty)_ | Optional key prefix inside the bucket. |
| `ARTIFACT_S3_ENDPOINT` | _(empty)_ | Custom S3-compatible endpoint (e.g. MinIO). Leave empty for AWS S3 default. |
| `ARTIFACT_S3_REGION` | `us-east-1` | AWS region for the S3 bucket. |
| `ARTIFACT_S3_ACCESS_KEY` | _(empty)_ | S3 access key ID. |
| `ARTIFACT_S3_SECRET_KEY` | _(empty)_ | S3 secret access key. |
| `ARTIFACT_S3_PATH_STYLE` | `true` | Use path-style addressing (`true` for MinIO, `false` for AWS virtual-hosted). |
| `ARTIFACT_S3_USE_SSL` | `false` | Enable HTTPS for the S3 connection. |
| `ARTIFACT_S3_CA_PATH` | _(none)_ | Path to PEM CA bundle for verifying the S3 endpoint certificate. |

---

## Telemetry

### Internal telemetry (Valkey aggregator)

| Variable | Default | Description |
|---|---|---|
| `TELEMETRY_BACKEND` | `none` | Telemetry recorder backend. Values: `none` (noop recorder + in-memory querier), `memory` (in-memory recorder + querier), `valkey` (persistent). |
| `TELEMETRY_KEY_PREFIX` | `workspace:telemetry` | Redis/Valkey key prefix for telemetry data. Only used when backend is `valkey`. |
| `TELEMETRY_TTL_SECONDS` | `604800` | Telemetry record TTL in seconds (default 7 days). Only used when backend is `valkey`. |
| `TELEMETRY_AGGREGATION_INTERVAL_SECONDS` | `300` | Background aggregation loop interval in seconds (default 5 min). Only used when backend is `valkey`. |

### OpenTelemetry (OTLP traces)

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_OTEL_ENABLED` | `false` | Enable the OTLP trace exporter. |
| `WORKSPACE_OTEL_EXPORTER_OTLP_ENDPOINT` | _(empty -- SDK default)_ | OTLP HTTP endpoint (e.g. `otel-collector:4318`). |
| `WORKSPACE_OTEL_EXPORTER_OTLP_INSECURE` | `false` | Disable TLS verification for the OTLP exporter (dev/test only). |
| `WORKSPACE_OTEL_TLS_CA_PATH` | _(none)_ | Path to PEM CA bundle for verifying the OTLP collector certificate. |
| `WORKSPACE_VERSION` | `unknown` | Reported as `service.version` in OTLP resource attributes. |
| `WORKSPACE_ENV` | `unknown` | Reported as `deployment.environment` in OTLP resource attributes. |

---

## Kubernetes Backend

All variables below apply only when `WORKSPACE_BACKEND=kubernetes` and the binary is built with the `k8s` build tag.

### Workspace pods

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_K8S_NAMESPACE` | `underpass-runtime` | Kubernetes namespace for workspace pods. |
| `WORKSPACE_K8S_SERVICE_ACCOUNT` | _(empty)_ | ServiceAccount assigned to workspace pods. Empty means the namespace default. |
| `WORKSPACE_K8S_RUNNER_IMAGE` | _(empty)_ | Default container image for workspace runner pods. |
| `WORKSPACE_K8S_RUNNER_IMAGE_BUNDLES_JSON` | _(empty)_ | JSON object mapping runner profile names to container images (e.g. `{"python":"img:py","node":"img:node"}`). |
| `WORKSPACE_K8S_RUNNER_PROFILE_METADATA_KEY` | `runner_profile` | Session metadata key used to select a runner image bundle. |
| `WORKSPACE_K8S_INIT_IMAGE` | _(empty)_ | Init container image (repo clone, workspace setup). |
| `WORKSPACE_K8S_WORKDIR` | `/workspace/repo` | Working directory inside the runner container. |
| `WORKSPACE_K8S_CONTAINER` | `runner` | Name of the runner container inside the pod spec. |
| `WORKSPACE_K8S_POD_PREFIX` | `ws` | Prefix for generated pod names (e.g. `ws-<session-id>`). |
| `WORKSPACE_K8S_READY_TIMEOUT_SECONDS` | `120` | Maximum wait time (seconds) for a workspace pod to reach Ready. |
| `WORKSPACE_K8S_GIT_AUTH_SECRET` | _(empty)_ | Name of the Kubernetes Secret containing Git credentials, mounted into the init container. |
| `WORKSPACE_K8S_GIT_AUTH_METADATA_KEY` | `git_auth_secret` | Session metadata key that overrides the default Git auth secret name. |

### Pod security context

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_K8S_RUN_AS_USER` | `1000` | UID for the pod security context `runAsUser`. |
| `WORKSPACE_K8S_RUN_AS_GROUP` | `1000` | GID for the pod security context `runAsGroup`. |
| `WORKSPACE_K8S_FS_GROUP` | `1000` | GID for the pod security context `fsGroup`. |
| `WORKSPACE_K8S_READ_ONLY_ROOT_FS` | `false` | Set `readOnlyRootFilesystem` on the runner container. |
| `WORKSPACE_K8S_AUTOMOUNT_SA_TOKEN` | `false` | Set `automountServiceAccountToken` on the pod spec. |

### Pod janitor (garbage collection)

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_K8S_POD_JANITOR_ENABLED` | `true` | Enable the background pod janitor loop. |
| `WORKSPACE_K8S_POD_JANITOR_INTERVAL_SECONDS` | `60` | Interval (seconds) between janitor sweeps. |
| `WORKSPACE_K8S_SESSION_POD_TERMINAL_TTL_SECONDS` | `300` | Grace period (seconds) before deleting a pod whose session has terminated. |
| `WORKSPACE_K8S_CONTAINER_POD_TERMINAL_TTL_SECONDS` | `300` | Grace period (seconds) before deleting a pod whose containers have terminated. |
| `WORKSPACE_K8S_MISSING_SESSION_GRACE_SECONDS` | `120` | Grace period (seconds) before deleting a pod whose session record no longer exists. |

### Kubernetes client

| Variable | Default | Description |
|---|---|---|
| `KUBECONFIG` | _(none)_ | Path to a kubeconfig file. Falls back to `~/.kube/config`, then in-cluster config. |

---

## Authentication

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_AUTH_MODE` | `payload` | Authentication mode. Values: `payload` (identity extracted from request body -- no token check), `trusted_headers` (identity from HTTP headers, validated with a shared token). |
| `WORKSPACE_AUTH_SHARED_TOKEN` | _(empty)_ | Shared secret token. Required when mode is `trusted_headers`. Compared in constant time. |
| `WORKSPACE_AUTH_TENANT_HEADER` | `X-Workspace-Tenant-Id` | HTTP header carrying the tenant identifier. |
| `WORKSPACE_AUTH_ACTOR_HEADER` | `X-Workspace-Actor-Id` | HTTP header carrying the actor (user/agent) identifier. |
| `WORKSPACE_AUTH_ROLES_HEADER` | `X-Workspace-Roles` | HTTP header carrying a comma-separated list of roles. |
| `WORKSPACE_AUTH_TOKEN_HEADER` | `X-Workspace-Auth-Token` | HTTP header carrying the shared authentication token. |

---

## Docker Backend

All variables below apply only when `WORKSPACE_BACKEND=docker`.

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_DOCKER_SOCKET` | _(empty -- Docker default)_ | Path to the Docker daemon socket (e.g. `/var/run/docker.sock`). Empty uses the client library default from `DOCKER_HOST`. |
| `WORKSPACE_DOCKER_IMAGE` | `alpine:3.20` | Default container image for workspace containers. |
| `WORKSPACE_DOCKER_IMAGE_BUNDLES_JSON` | _(empty)_ | JSON object mapping runner profile names to container images. Same semantics as the K8s equivalent. |
| `WORKSPACE_DOCKER_RUNNER_PROFILE_KEY` | `runner_profile` | Session metadata key used to select an image bundle. |
| `WORKSPACE_DOCKER_WORKDIR` | `/workspace/repo` | Working directory inside the workspace container. |
| `WORKSPACE_DOCKER_CONTAINER_PREFIX` | `ws` | Prefix for generated container names. |
| `WORKSPACE_DOCKER_NETWORK` | _(empty)_ | Docker network to attach workspace containers to. Empty means the default bridge. |
| `WORKSPACE_DOCKER_CPU_LIMIT` | `2` | CPU core limit for workspace containers (integer). |
| `WORKSPACE_DOCKER_MEMORY_LIMIT_MB` | `2048` | Memory limit in MiB for workspace containers. |
| `WORKSPACE_DOCKER_TTL_SECONDS` | `3600` | Container TTL in seconds (default 1 h). Containers exceeding this age may be reaped. |
