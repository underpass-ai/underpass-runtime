# TLS Architecture — underpass-runtime namespace

All services in the `underpass-runtime` namespace communicate via mutual TLS (mTLS).

## Certificate Authorities

| CA | CN | Scope | Secret |
|----|----|-------|--------|
| Runtime CA | `underpass-runtime-ca` | Runtime gRPC server + client auth | `runtime-tls` (ca.crt) |
| Kernel CA | `rehydration-kernel-internal-ca` | NATS, Valkey, Neo4j, MinIO | `rehydration-kernel-internal-ca` |

## Service Certificates

| Service | Port | TLS Mode | Server Cert Secret | CA |
|---------|------|----------|-------------------|-----|
| underpass-runtime (gRPC) | 50053 | mutual | `runtime-tls` | Runtime CA |
| rehydration-kernel (gRPC) | 50054 | mutual | `rehydration-kernel-grpc-tls` | Kernel CA |
| NATS | 4222 | mutual | `rehydration-kernel-nats-tls` | Kernel CA |
| Valkey | 6379 | mutual | `rehydration-kernel-valkey-tls` | Kernel CA |
| Neo4j | 7687 | server | `rehydration-kernel-neo4j-tls` | Kernel CA |
| MinIO (S3) | 9000 | server | `minio-tls` | Kernel CA |
| Prometheus metrics | 9090 | none | — | — |

## Client Certificates (runtime → kernel infra)

| Client | Destination | Client Cert Secret | Signed By |
|--------|-------------|-------------------|-----------|
| runtime → NATS | `rehydration-kernel-nats:4222` | `runtime-nats-client-tls` | Kernel CA |
| runtime → Valkey | `rehydration-kernel-valkey:6379` | `runtime-valkey-client-tls` | Kernel CA |
| runtime → MinIO | `minio:9000` | `runtime-s3-client-tls` | Kernel CA |

## Connection Map

```
                        ┌─────────────────────────────┐
  Agents ──── mTLS ────>│  underpass-runtime (gRPC)    │
  (client cert from     │  Port 50053                  │
   runtime CA)          │  Server: runtime-tls         │
                        └──┬──────────┬──────────┬─────┘
                           │          │          │
                    mTLS   │   mTLS   │   TLS    │
                           │          │          │
                    ┌──────▼──┐ ┌─────▼────┐ ┌───▼────┐
                    │  NATS   │ │  Valkey   │ │  MinIO │
                    │  :4222  │ │  :6379    │ │  :9000 │
                    └─────────┘ └──────────┘ └────────┘
                    Kernel CA   Kernel CA     Kernel CA
```

## Helm Configuration

### Runtime (values.shared-infra.yaml pattern)

```yaml
tls:
  mode: mutual
  existingSecret: runtime-tls

valkey:
  enabled: true
  host: rehydration-kernel-valkey

valkeyTls:
  enabled: true
  existingSecret: runtime-valkey-client-tls
  keys: {ca: ca.crt, cert: tls.crt, key: tls.key}

eventBus:
  type: nats
  nats:
    url: nats://rehydration-kernel-nats:4222

natsTls:
  mode: mutual
  existingSecret: runtime-nats-client-tls
  keys: {ca: ca.crt, cert: tls.crt, key: tls.key}
```

### Kernel (values.underpass-runtime.yaml)

```yaml
nats:
  enabled: true
  tls:
    enabled: true
    existingSecret: rehydration-kernel-nats-tls

valkey:
  enabled: true
  tls:
    enabled: true
    existingSecret: rehydration-kernel-valkey-tls
```

## Certificate Generation

Certificates are currently generated manually. See `memory/project_certs_reorg.md`
for the planned migration to automated cert management.

### Quick Reference

```bash
# Generate client cert for runtime, signed by kernel CA
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
  -keyout client.key -out client.csr -subj "/CN=underpass-runtime"

openssl x509 -req -in client.csr \
  -CA kernel-ca.crt -CAkey kernel-ca.key -CAcreateserial \
  -out client.crt -days 365 \
  -extfile <(echo "subjectAltName=DNS:underpass-runtime,DNS:underpass-runtime.underpass-runtime.svc.cluster.local")

kubectl create secret generic runtime-nats-client-tls \
  -n underpass-runtime \
  --from-file=tls.crt=client.crt \
  --from-file=tls.key=client.key \
  --from-file=ca.crt=kernel-ca.crt
```

## Health Probes

gRPC health probes cannot handle TLS. When `tls.mode != disabled`:
- Liveness: `tcpSocket` on port 50053
- Readiness: `tcpSocket` on port 50053

When TLS is disabled:
- Native `grpc` probe on port 50053
