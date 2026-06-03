# TLS Architecture — underpass-runtime namespace

All services in the `underpass-runtime` namespace communicate via mutual TLS (mTLS).

## Certificate Authorities

A single shared internal CA signs **every** certificate in the namespace —
including the runtime's own gRPC server and client certs. There is no separate
"runtime CA": the runtime certs are signed by the kernel CA via the cert-gen
hook (see [Certificate Generation](#certificate-generation)).

| CA | CN | Scope | Secret |
|----|----|-------|--------|
| Kernel CA (shared) | `rehydration-kernel-internal-ca` | All mTLS in the namespace: runtime gRPC, NATS, Valkey, Neo4j, MinIO | `rehydration-kernel-internal-ca` (`tls.crt` + `tls.key`) |

## Service Certificates

| Service | Port | TLS Mode | Server Cert Secret | CA |
|---------|------|----------|-------------------|-----|
| underpass-runtime (gRPC) | 50053 | mutual | `runtime-tls` | Kernel CA |
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

Certificates are generated **automatically** by the cert-gen Helm hook
(`certGen.enabled=true`) — a `pre-install`/`pre-upgrade` Job that reads the
shared CA from `certGen.caSecret` (default `rehydration-kernel-internal-ca`) and
signs four secrets idempotently (skipping any that already exist):

| Secret | Purpose | Key Usage |
|--------|---------|-----------|
| `{fullname}-tls` | Runtime gRPC server cert | serverAuth, clientAuth |
| `{fullname}-nats-client-tls` | NATS client cert | clientAuth |
| `{fullname}-valkey-client-tls` | Valkey client cert | clientAuth |
| `{fullname}-s3-client-tls` | S3/MinIO client cert | clientAuth |

Each secret contains `tls.crt`, `tls.key`, and `ca.crt` (ECDSA P-256, 365-day
validity by default). To rotate, delete the target secret and run `helm upgrade`.
See [deployment-tls.md](../operations/deployment-tls.md) for the full guide.

### Manual fallback (without cert-gen)

If you are not using the cert-gen hook, sign certs against the shared CA by hand:

```bash
# Generate client cert for runtime, signed by the shared CA
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
