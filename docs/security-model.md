# Security Model

This document describes the security architecture of underpass-runtime: trust
boundaries, transport security, authorization model, and known gaps.

---

## Transport Security Posture

```
                          ┌─────────────────────────────────────┐
                          │          underpass-runtime           │
                          │                                     │
  Callers ──── HTTPS ────▶│  HTTP server (TLS 1.3 min)          │
  (agents,                │                                     │
   fleet-proxy,           │  ┌───────────────────────────────┐  │
   CLI)                   │  │ Policy Engine + Audit Logger   │  │
                          │  └───────────────────────────────┘  │
                          │                                     │
                          │  ┌────────┐  ┌───────┐  ┌───────┐  │
                          │  │ Valkey  │  │ NATS  │  │ S3    │  │
                          │  │ (TLS)  │  │ (TLS) │  │ (TLS) │  │
                          │  └───┬────┘  └───┬───┘  └───┬───┘  │
                          └──────┼───────────┼──────────┼──────┘
                                 │           │          │
                          ┌──────▼──┐  ┌─────▼──┐  ┌───▼────┐
                          │ Valkey  │  │  NATS  │  │ MinIO  │
                          │ cluster │  │ server │  │   S3   │
                          └─────────┘  └────────┘  └────────┘
```

---

## Trust Boundaries

| Boundary | Transport | Auth | TLS Modes | Implementation |
|---|---|---|---|---|
| **Caller → Runtime** | HTTPS | Shared token (`trusted_headers`) or payload identity | `disabled`, `server`, `mutual` | `internal/tlsutil/tls.go` — TLS 1.3 min, `RequireAndVerifyClientCert` for mutual |
| **Runtime → Valkey** | TCP/TLS | Password (AUTH) | `disabled`, `server` (CA-only), `mutual` (client cert) | `go-redis/v9` TLS config with explicit CA, cert, key |
| **Runtime → NATS** | TCP/TLS | None (network trust) | `disabled`, `server`, `mutual` | `nats.go` with `tls://` scheme, CA pinning |
| **Runtime → S3/MinIO** | HTTPS | Access key + secret key | `disabled` (HTTP), `server` (HTTPS + CA) | `aws-sdk-go-v2` with custom CA bundle |
| **Runtime → OTLP** | HTTP(S) | None | `disabled` (insecure), `server` (CA-only) | OTLP HTTP exporter with CA path |
| **Runtime → Workspace Pod** | K8s API | ServiceAccount token | In-cluster TLS (automatic) | `client-go` with in-cluster config |

---

## Authorization Model

### Layers

Tool invocations pass through three authorization layers before execution:

```
Request → Auth Middleware → Policy Engine → Tool Invoker → Audit Logger
```

1. **Auth Middleware** (`internal/httpapi/auth.go`):
   - Mode `payload`: identity extracted from request body (no token check — local dev only).
   - Mode `trusted_headers`: shared token in `X-Workspace-Auth-Token`, compared in constant-time. Principal identity from `X-Workspace-Tenant-Id`, `X-Workspace-Actor-Id`, `X-Workspace-Roles`.

2. **Policy Engine** (`internal/adapters/policy/static_policy.go`):
   - **Role-based access**: principal roles matched against capability `allowed_roles`.
   - **Risk gating**: capabilities with `requires_approval: true` require explicit `approved: true` in request.
   - **Path restrictions**: workspace-scoped tools validated against `allowed_paths` from session config.
   - **Scope enforcement**: capability scope (`session`, `global`) determines whether session context is required.
   - **Rate limiting**: per-session and per-principal invocation quotas.
   - **Namespace/registry allow-lists**: container and K8s tools restricted to approved namespaces, registries, and resource types.

3. **Audit Logger** (`internal/adapters/audit/logger_audit.go`):
   - All invocations recorded with session, tool, actor, tenant, status.
   - Sensitive metadata redacted via regex patterns: tokens, passwords, API keys, bearer tokens, credentials in URLs.

### Policy Decision Flow

```
PolicyInput {Session, Capability, Args, Approved}
    │
    ├── Check role membership ──── denied → {Allow: false, ErrorCode: "role_denied"}
    ├── Check risk + approval ──── denied → {Allow: false, ErrorCode: "approval_required"}
    ├── Check path restrictions ── denied → {Allow: false, ErrorCode: "path_denied"}
    ├── Check rate limits ──────── denied → {Allow: false, ErrorCode: "rate_limited"}
    └── All passed ─────────────── {Allow: true}
```

---

## Workspace Isolation

| Backend | Isolation Level | Mechanism |
|---|---|---|
| **local** | Filesystem | Unique directory per session under `WORKSPACE_ROOT/<tenant>/<session>/repo`. Cleanup on session close. |
| **docker** | Container | Dedicated container per session. CPU/memory limits. Network isolation via Docker network config. Container TTL with reaping. |
| **kubernetes** | Pod | Dedicated pod per session. `SecurityContext`: `runAsNonRoot`, `readOnlyRootFilesystem`, `drop ALL` capabilities. `NetworkPolicy` isolation. Pod janitor garbage-collects orphans. |

### Container Security Defaults

The runtime itself runs with:
- **Image**: `gcr.io/distroless/static-debian12:nonroot` — no shell, no package manager
- **UID**: 65532 (nonroot)
- **Root filesystem**: read-only
- **Capabilities**: ALL dropped
- **Seccomp**: RuntimeDefault profile
- **Privilege escalation**: disabled

---

## Threat Model

| # | Threat | Mitigation | Status |
|---|---|---|---|
| T1 | **Unauthenticated API access** | `trusted_headers` mode: shared token validated in constant-time. `mutual` TLS: client certificate required. | Implemented |
| T2 | **Man-in-the-middle** | TLS 1.3 minimum on all transports. CA pinning for Valkey, NATS, S3. | Implemented |
| T3 | **Unauthorized tool execution** | Policy engine: role-based access, risk gating, approval workflows. | Implemented |
| T4 | **Path traversal** | Policy engine validates args against `allowed_paths`. `resolvePath()` canonicalizes and rejects `..` escapes. | Implemented |
| T5 | **Privilege escalation in workspace** | Pods run as non-root (UID 1000). Capabilities dropped. Read-only root FS. | Implemented |
| T6 | **Sensitive data in logs** | Audit logger redacts tokens, passwords, API keys, bearer tokens, URL credentials via regex. | Implemented |
| T7 | **Orphaned workspace resources** | Pod janitor: background loop reaps terminated/orphaned pods. Docker backend: TTL-based reaping. | Implemented |
| T8 | **Supply chain vulnerability** | `govulncheck` in CI. CodeQL SAST. SonarCloud quality gate. Distroless base image. | Implemented |
| T9 | **Excessive resource consumption** | Per-session rate limits. Container CPU/memory limits. HPA available. | Implemented |
| T10 | **Secret leakage via artifacts** | Artifacts stored in isolated S3 buckets with per-service IAM policies. Lifecycle rules auto-expire (7d artifacts, 30d telemetry, 90d audit). | Implemented |

---

## Known Gaps

| Gap | Severity | Status | Notes |
|---|---|---|---|
| **OTLP export is plaintext by default** | Medium | Configurable | Set `telemetry.otel.caPath` to enable TLS. Insecure mode exists for dev. |
| **`payload` auth mode has no token verification** | Low | By design | Intended for local development only. Production must use `trusted_headers` or `mutual` TLS. |
| **No per-tool audit retention policy** | Low | Planned | Audit events follow Valkey TTL. No tool-specific retention differentiation. |
| **Network policies disabled by default** | Medium | Configurable | `networkPolicy.enabled: true` in Helm values activates egress restrictions to Valkey, NATS, S3, K8s API, DNS. |
| **govulncheck is non-blocking in CI** | Low | Intentional | Known upstream Go vulnerabilities tracked. Will become blocking when false-positive rate stabilizes. |

---

## TLS Configuration Reference

All transports enforce **TLS 1.3 minimum** (hard-coded in `internal/tlsutil/tls.go`).

| Transport | Env Var (Mode) | Modes | Cert Source |
|---|---|---|---|
| HTTP server | `WORKSPACE_TLS_MODE` | `disabled`, `server`, `mutual` | K8s Secret → volume mount |
| Valkey | `VALKEY_TLS_ENABLED` | `true`/`false` + optional client cert | K8s Secret → volume mount |
| NATS | `NATS_TLS_MODE` | `disabled`, `server`, `mutual` | K8s Secret → volume mount |
| S3/MinIO | `ARTIFACT_S3_USE_SSL` | `true`/`false` + optional CA | K8s Secret → volume mount or system CA |
| OTLP | `WORKSPACE_OTEL_TLS_CA_PATH` | CA path or insecure | K8s Secret → volume mount or system CA |

See [DEPLOYMENT-TLS.md](DEPLOYMENT-TLS.md) for step-by-step deployment instructions.
