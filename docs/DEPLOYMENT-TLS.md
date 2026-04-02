# TLS Deployment Guide for underpass-runtime

This guide covers enabling TLS across all five transports in underpass-runtime:
HTTP server, NATS event bus, Valkey session/invocation store, S3/MinIO artifact
storage, and OTLP telemetry export.

## Table of Contents

1. [Overview](#1-overview)
2. [Automated Certificate Generation (Recommended)](#2-automated-certificate-generation-recommended)
3. [Manual Certificate Generation](#3-manual-certificate-generation)
4. [Create Kubernetes Secrets](#4-create-kubernetes-secrets)
5. [Deploy with Server TLS (tls.mode=server)](#5-deploy-with-server-tls)
6. [Deploy with Mutual TLS (tls.mode=mutual)](#6-deploy-with-mutual-tls)
7. [Valkey TLS Setup](#7-valkey-tls-setup)
8. [NATS TLS Setup](#8-nats-tls-setup)
9. [S3/MinIO TLS](#9-s3minio-tls)
10. [OTLP TLS](#10-otlp-tls)
11. [Verification](#11-verification)
12. [Troubleshooting](#12-troubleshooting)

---

## 1. Overview

### TLS Modes

underpass-runtime supports three TLS modes, controlled by the `tls.mode` Helm
value (or the `WORKSPACE_TLS_MODE` environment variable):

| Mode       | Aliases            | Behaviour                                                  |
|------------|--------------------|------------------------------------------------------------|
| `disabled` | `plaintext`, `""`  | No TLS. Plain HTTP on the configured port.                 |
| `server`   | `tls`              | Server presents a certificate. Clients verify it.          |
| `mutual`   | `mtls`             | Server presents a certificate AND requires a client cert.  |

### TLS 1.3 Minimum

All TLS configurations enforce **TLS 1.3 as the minimum version**. This is
hard-coded in `internal/tlsutil/tls.go` and applies to every transport (HTTP
server, NATS client, Valkey client, S3 client, OTLP client). Connections from
clients that do not support TLS 1.3 will be rejected.

### Environment Variables Reference

| Transport   | Variables                                                                                      |
|-------------|-----------------------------------------------------------------------------------------------|
| HTTP server | `WORKSPACE_TLS_MODE`, `WORKSPACE_TLS_CERT_PATH`, `WORKSPACE_TLS_KEY_PATH`, `WORKSPACE_TLS_CLIENT_CA_PATH` |
| Valkey      | `VALKEY_TLS_ENABLED`, `VALKEY_TLS_CA_PATH`, `VALKEY_TLS_SERVER_NAME`, `VALKEY_TLS_CERT_PATH`, `VALKEY_TLS_KEY_PATH` |
| NATS        | `NATS_TLS_MODE`, `NATS_TLS_CA_PATH`, `NATS_TLS_SERVER_NAME`, `NATS_TLS_CERT_PATH`, `NATS_TLS_KEY_PATH`, `NATS_TLS_FIRST` |
| S3/MinIO    | `ARTIFACT_S3_USE_SSL`, `ARTIFACT_S3_CA_PATH`                                                  |
| OTLP        | `WORKSPACE_OTEL_TLS_CA_PATH`                                                                  |

---

## 2. Automated Certificate Generation (Recommended)

The runtime Helm chart includes a pre-install/pre-upgrade hook Job that
automatically generates all required TLS certificates from a shared CA.

### Prerequisites

The shared CA secret must exist in the namespace before deploying the runtime.
It is created by the `rehydration-kernel` chart (with `certGen.enabled: true`)
or manually:

```bash
# Manual CA creation (if not using the kernel's cert-gen)
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:prime256v1 -out ca.key
openssl req -new -x509 -key ca.key -out ca.crt -days 3650 \
  -subj "/CN=rehydration-kernel-internal-ca"
kubectl create secret generic rehydration-kernel-internal-ca \
  --from-file=tls.crt=ca.crt --from-file=tls.key=ca.key --from-file=ca.crt \
  -n underpass-runtime
```

### Enable cert-gen

```yaml
# In your values override:
certGen:
  enabled: true
  caSecret: rehydration-kernel-internal-ca  # default
```

The Job creates three secrets (idempotent — skips if they already exist):

| Secret | Purpose | Key Usage |
|--------|---------|-----------|
| `{fullname}-tls` | Runtime gRPC server cert | serverAuth, clientAuth |
| `{fullname}-nats-client-tls` | NATS client cert | clientAuth |
| `{fullname}-valkey-client-tls` | Valkey client cert | clientAuth |

Each secret contains `tls.crt`, `tls.key`, and `ca.crt`. Certificates use
ECDSA P-256 keys with 365-day validity by default.

### Rotation

To rotate a certificate, delete the target secret and run `helm upgrade`. The
Job detects the missing secret and regenerates only that one, signed by the
same CA:

```bash
kubectl delete secret underpass-runtime-nats-client-tls -n underpass-runtime
helm upgrade underpass-runtime charts/underpass-runtime ...
```

### Configuration Reference

| Value | Default | Description |
|-------|---------|-------------|
| `certGen.enabled` | `false` | Enable the cert-gen hook Job |
| `certGen.image` | `docker.io/alpine:3.19` | Container image (needs openssl, curl, jq) |
| `certGen.caSecret` | `rehydration-kernel-internal-ca` | Name of the CA secret to read |
| `certGen.keyCurve` | `prime256v1` | ECDSA curve for generated keys |
| `certGen.validityDays` | `365` | Certificate validity in days |

---

## 3. Manual Certificate Generation

> **Note:** If you enabled `certGen` in section 2, skip to section 5.

The commands below create a self-signed CA and a server certificate with a
Subject Alternative Name (SAN). Adjust the SAN values to match your cluster's
service DNS names.

### 2a. Create a Certificate Authority (CA)

```bash
# Generate CA private key
openssl genrsa -out ca.key 4096

# Generate CA certificate (valid 10 years)
openssl req -new -x509 -days 3650 -key ca.key \
  -out ca.crt \
  -subj "/CN=underpass-runtime-ca/O=Underpass"
```

### 2b. Create a Server Certificate

```bash
# Generate server private key
openssl genrsa -out tls.key 4096

# Create a CSR config with SANs
cat > server-csr.conf <<EOF
[req]
default_bits       = 4096
prompt             = no
distinguished_name = dn
req_extensions     = v3_req

[dn]
CN = underpass-runtime
O  = Underpass

[v3_req]
subjectAltName = @alt_names

[alt_names]
DNS.1 = underpass-runtime
DNS.2 = underpass-runtime.default.svc
DNS.3 = underpass-runtime.default.svc.cluster.local
DNS.4 = localhost
IP.1  = 127.0.0.1
EOF

# Generate CSR
openssl req -new -key tls.key -out tls.csr -config server-csr.conf

# Sign with the CA (valid 1 year)
openssl x509 -req -days 365 \
  -in tls.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out tls.crt \
  -extensions v3_req \
  -extfile server-csr.conf
```

Replace the `DNS.*` entries with the actual Kubernetes service names in your
namespace. The pattern is `<release>-underpass-runtime.<namespace>.svc.cluster.local`.

### 2c. Create a Client Certificate (for mutual TLS)

Only needed when `tls.mode=mutual`.

```bash
openssl genrsa -out client.key 4096

cat > client-csr.conf <<EOF
[req]
default_bits       = 4096
prompt             = no
distinguished_name = dn

[dn]
CN = underpass-runtime-client
O  = Underpass
EOF

openssl req -new -key client.key -out client.csr -config client-csr.conf

openssl x509 -req -days 365 \
  -in client.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out client.crt
```

---

## 3. Create Kubernetes Secrets

### 3a. HTTP Server TLS Secret

For `server` mode (cert + key + CA for helm test verification):

```bash
kubectl create secret generic underpass-runtime-tls \
  --from-file=tls.crt=tls.crt \
  --from-file=tls.key=tls.key \
  --from-file=ca.crt=ca.crt
```

For `mutual` mode the same secret works -- the `ca.crt` is used as the client
CA to verify incoming client certificates.

### 3b. NATS TLS Secret

```bash
kubectl create secret generic underpass-runtime-nats-tls \
  --from-file=ca.crt=ca.crt \
  --from-file=tls.crt=client.crt \
  --from-file=tls.key=client.key
```

### 3c. Valkey TLS Secret

```bash
kubectl create secret generic underpass-runtime-valkey-tls \
  --from-file=ca.crt=ca.crt \
  --from-file=tls.crt=client.crt \
  --from-file=tls.key=client.key
```

---

## 4. Deploy with Server TLS

Create a values file `values-tls-server.yaml`:

```yaml
tls:
  mode: server
  existingSecret: underpass-runtime-tls
  mountPath: /var/run/underpass-runtime/tls
  keys:
    cert: tls.crt
    key: tls.key
    clientCa: ca.crt   # used by helm test to verify server cert
```

Deploy:

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -f values-tls-server.yaml
```

What this does:

- Sets `WORKSPACE_TLS_MODE=server` in the ConfigMap.
- Mounts the secret at `/var/run/underpass-runtime/tls`.
- Sets `WORKSPACE_TLS_CERT_PATH` and `WORKSPACE_TLS_KEY_PATH` to the mounted paths.
- Changes the service port name from `http` to `https`.
- Switches liveness/readiness probes to `scheme: HTTPS`.

---

## 5. Deploy with Mutual TLS

Create a values file `values-tls-mutual.yaml`:

```yaml
tls:
  mode: mutual
  existingSecret: underpass-runtime-tls
  mountPath: /var/run/underpass-runtime/tls
  keys:
    cert: tls.crt
    key: tls.key
    clientCa: ca.crt
```

Deploy:

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -f values-tls-mutual.yaml
```

What changes compared to `server` mode:

- Sets `WORKSPACE_TLS_MODE=mutual`.
- Sets `WORKSPACE_TLS_CLIENT_CA_PATH` to the mounted `ca.crt`.
- The server requires and verifies client certificates (`RequireAndVerifyClientCert`).
- Liveness/readiness probes switch to `tcpSocket` because the kubelet cannot
  present a client certificate for HTTP probes.
- The helm test uses `nc` (netcat) TCP connectivity check instead of `curl`.

### Client-side server name verification

For NATS and Valkey, the runtime can override the TLS server name used for SNI
and certificate hostname verification:

- `NATS_TLS_SERVER_NAME`
- `VALKEY_TLS_SERVER_NAME`

This is useful when the runtime connects through one Kubernetes service name or
alias, but must verify a certificate issued for a different SAN, for example a
dependency managed by another Helm release.

### Helm Validation Guards

The chart includes fail-fast validation in `_helpers.tpl`. If you set
`tls.mode=server` or `tls.mode=mutual` without providing `tls.existingSecret`,
`tls.mountPath`, or the required key names, `helm template` / `helm install`
will fail immediately with a descriptive error. For example:

- `tls.existingSecret is required when tls.mode is server or mutual`
- `tls.keys.clientCa is required when tls.mode=mutual`

---

## 6. Valkey TLS Setup

Valkey TLS uses a boolean enable flag rather than a mode string.

Example:

```yaml
valkeyTls:
  enabled: true
  existingSecret: underpass-runtime-valkey-tls
  serverName: rehydration-kernel-valkey
  mountPath: /var/run/underpass-runtime/valkey-tls
  keys:
    ca: ca.crt
    cert: tls.crt
    key: tls.key
```

This renders:

- `VALKEY_TLS_ENABLED=true`
- `VALKEY_TLS_CA_PATH=/var/run/underpass-runtime/valkey-tls/ca.crt`
- `VALKEY_TLS_SERVER_NAME=rehydration-kernel-valkey`
- `VALKEY_TLS_CERT_PATH=/var/run/underpass-runtime/valkey-tls/tls.crt`
- `VALKEY_TLS_KEY_PATH=/var/run/underpass-runtime/valkey-tls/tls.key`

### Environment Variables

| Variable              | Description                                    |
|-----------------------|------------------------------------------------|
| `VALKEY_TLS_ENABLED`  | Set to `true` to enable TLS for Valkey.        |
| `VALKEY_TLS_CA_PATH`  | Path to CA certificate for server verification.|
| `VALKEY_TLS_SERVER_NAME` | Optional SNI / hostname verification override for the Valkey certificate. |
| `VALKEY_TLS_CERT_PATH`| Path to client certificate (mTLS only).        |
| `VALKEY_TLS_KEY_PATH` | Path to client private key (mTLS only).        |

### Helm Values -- CA-only (server verification)

```yaml
valkey:
  enabled: true
  host: valkey
  port: 6380          # Valkey TLS typically uses 6380
  existingSecret: valkey-password

valkeyTls:
  enabled: true
  existingSecret: underpass-runtime-valkey-tls
  mountPath: /var/run/underpass-runtime/valkey-tls
  keys:
    ca: ca.crt
    cert: ""           # empty = no client cert
    key: ""
```

### Helm Values -- Mutual TLS

```yaml
valkeyTls:
  enabled: true
  existingSecret: underpass-runtime-valkey-tls
  mountPath: /var/run/underpass-runtime/valkey-tls
  keys:
    ca: ca.crt
    cert: tls.crt
    key: tls.key
```

Validation guards enforce:

- `valkeyTls.existingSecret` is required when any `valkeyTls.keys.*` are set.
- `valkeyTls.mountPath` is required when `valkeyTls.existingSecret` is set.
- `cert` and `key` must both be set or both be empty.

---

## 7. NATS TLS Setup

### Environment Variables

| Variable            | Description                                              |
|---------------------|----------------------------------------------------------|
| `NATS_TLS_MODE`     | `disabled`, `server`, or `mutual`.                       |
| `NATS_TLS_CA_PATH`  | Path to CA certificate for NATS server verification.     |
| `NATS_TLS_SERVER_NAME` | Optional SNI / hostname verification override for the NATS certificate. |
| `NATS_TLS_CERT_PATH`| Path to client certificate (mutual only).                |
| `NATS_TLS_KEY_PATH` | Path to client private key (mutual only).                |
| `NATS_TLS_FIRST`    | **Not supported.** See limitation below.                 |

### Helm Values -- Server Verification

```yaml
eventBus:
  type: nats
  nats:
    url: tls://nats:4222    # use tls:// scheme

natsTls:
  mode: server
  existingSecret: underpass-runtime-nats-tls
  serverName: rehydration-kernel-nats
  mountPath: /var/run/underpass-runtime/nats-tls
  keys:
    ca: ca.crt
    cert: ""
    key: ""
```

### Helm Values -- Mutual TLS

```yaml
natsTls:
  mode: mutual
  existingSecret: underpass-runtime-nats-tls
  serverName: rehydration-kernel-nats
  mountPath: /var/run/underpass-runtime/nats-tls
  keys:
    ca: ca.crt
    cert: tls.crt
    key: tls.key
```

### TLS_FIRST Limitation

The `NATS_TLS_FIRST` environment variable is read for compatibility with the
Rust rehydration-kernel, but the Go `nats.go` client library does **not**
support the TLS-first handshake. If set to `true`, the service logs a warning
and ignores the flag:

```
WARN  NATS_TLS_FIRST=true requested but Go nats.go client does not support TLS-first handshake; flag ignored
```

Use the `tls://` URL scheme in `eventBus.nats.url` instead of relying on
TLS_FIRST.

### Validation Guards

- `natsTls.existingSecret` is required when `natsTls.keys.*` are configured.
- `natsTls.mountPath` is required when `natsTls.existingSecret` is set.
- `natsTls.mode=mutual` requires `existingSecret`, `keys.cert`, and `keys.key`.
- `cert` and `key` must both be set or both be empty.

---

## 8. S3/MinIO TLS

S3/MinIO TLS is controlled by two environment variables. There is no separate
Helm TLS section -- the values live under `artifacts.s3`.

### Environment Variables

| Variable               | Description                                         |
|------------------------|-----------------------------------------------------|
| `ARTIFACT_S3_USE_SSL`  | Set to `true` to use HTTPS for S3 connections.      |
| `ARTIFACT_S3_CA_PATH`  | Path to CA certificate for custom/private S3 CAs.   |

### Helm Values

```yaml
artifacts:
  backend: s3
  s3:
    bucket: workspace-artifacts
    endpoint: minio.default.svc.cluster.local:9000
    region: us-east-1
    pathStyle: true
    useSSL: true
    caPath: /var/run/underpass-runtime/tls/ca.crt   # reuse the server TLS mount
    existingSecret: minio-credentials
```

If your MinIO instance uses a certificate signed by the same CA as the HTTP
server, you can point `caPath` at the same mounted `ca.crt`. Otherwise, create
a separate secret and mount it.

For AWS S3 (using public CAs), set `useSSL: true` and leave `caPath` empty --
the system CA bundle will be used.

---

## 9. OTLP TLS

OpenTelemetry OTLP export uses a single CA path variable for TLS verification.

### Environment Variables

| Variable                     | Description                                    |
|------------------------------|------------------------------------------------|
| `WORKSPACE_OTEL_TLS_CA_PATH`| Path to CA certificate for OTLP collector TLS. |

### Helm Values

```yaml
telemetry:
  backend: valkey        # or memory
  otel:
    enabled: true
    endpoint: otel-collector.observability.svc.cluster.local:4317
    insecure: false      # must be false for TLS
    caPath: /var/run/underpass-runtime/tls/ca.crt
```

If the OTLP collector uses a publicly-trusted certificate, leave `caPath`
empty and set `insecure: false`. Setting `insecure: true` disables TLS
entirely for the OTLP exporter (use only for development).

---

## 10. Verification

### 10a. Helm Template Validation

Dry-run to verify all validation guards pass before deploying:

```bash
helm template underpass-runtime charts/underpass-runtime \
  -f values-tls-server.yaml
```

If any required TLS field is missing, the command will fail with a descriptive
error (e.g., `tls.existingSecret is required when tls.mode is server or mutual`).

### 10b. Helm Test

After deploying, run the built-in connectivity test:

```bash
helm test underpass-runtime
```

The test pod behaviour depends on the TLS mode:

| Mode       | Test Strategy                                    |
|------------|--------------------------------------------------|
| `disabled` | `wget --spider http://<svc>:<port>/healthz`      |
| `server`   | `curl --cacert <ca.crt> https://<svc>:<port>/healthz` |
| `mutual`   | `nc -z -w5 <svc> <port>` (TCP connectivity only) |

### 10c. Manual Verification with curl

From inside the cluster (e.g., a debug pod):

**Server TLS:**

```bash
# Copy ca.crt into the debug pod, then:
curl --cacert ca.crt \
  https://underpass-runtime.default.svc.cluster.local:50053/healthz
```

**Mutual TLS:**

```bash
curl --cacert ca.crt \
  --cert client.crt \
  --key client.key \
  https://underpass-runtime.default.svc.cluster.local:50053/healthz
```

### 10d. Verify TLS 1.3

```bash
openssl s_client \
  -connect underpass-runtime.default.svc.cluster.local:50053 \
  -tls1_3 \
  -CAfile ca.crt \
  </dev/null 2>&1 | grep "Protocol"
```

Expected output:

```
Protocol  : TLSv1.3
```

Attempting TLS 1.2 should fail:

```bash
openssl s_client \
  -connect underpass-runtime.default.svc.cluster.local:50053 \
  -tls1_2 \
  -CAfile ca.crt \
  </dev/null 2>&1 | grep -i "error\|alert"
```

---

## 11. Troubleshooting

### "tls.existingSecret is required when tls.mode is server or mutual"

The Helm chart requires `tls.existingSecret` whenever TLS is enabled. Create
the Kubernetes secret first (see section 3), then reference it in your values.

### "no valid certificates in CA file /var/run/..."

The CA file is not valid PEM or is empty. Verify the secret contents:

```bash
kubectl get secret underpass-runtime-tls -o jsonpath='{.data.ca\.crt}' | base64 -d | openssl x509 -noout -text
```

### "load server cert/key: tls: private key does not match public key"

The certificate and key in the secret do not form a valid pair. Verify:

```bash
openssl x509 -noout -modulus -in tls.crt | md5sum
openssl rsa  -noout -modulus -in tls.key | md5sum
```

Both checksums must match.

### "remote error: tls: bad certificate" (mutual TLS)

The client is either not presenting a certificate or presenting one that is not
signed by the CA specified in `tls.keys.clientCa`. Verify the client cert:

```bash
openssl verify -CAfile ca.crt client.crt
```

### Liveness/readiness probes fail with TLS enabled

In `server` mode, the probes use `scheme: HTTPS`. If the certificate's SAN
does not include the pod IP or service DNS, the kubelet's probe will fail with
a TLS error. Make sure the certificate SANs cover the service name.

In `mutual` mode, probes fall back to `tcpSocket` because the kubelet cannot
present a client certificate. This is expected and handled automatically by the
chart.

### NATS connection fails with "tls: first record does not look like a TLS handshake"

The NATS server expects a NATS INFO handshake before TLS upgrade, but the
client is attempting TLS immediately. Use the `tls://` URL scheme in
`eventBus.nats.url` and do **not** rely on `NATS_TLS_FIRST` (not supported in
the Go client).

### Valkey connection times out with TLS enabled

Valkey in TLS mode typically listens on port **6380**, not 6379. Verify
`valkey.port` matches the Valkey server's TLS port. Also confirm that Valkey
was started with TLS enabled (`--tls-port 6380 --tls-cert-file ... --tls-key-file ... --tls-ca-cert-file ...`).

### S3/MinIO "x509: certificate signed by unknown authority"

Set `artifacts.s3.caPath` to the path of the CA that signed the MinIO server
certificate. If reusing the HTTP server TLS mount, point to
`/var/run/underpass-runtime/tls/ca.crt`. Otherwise, mount a separate secret.

### OTLP export fails with "transport: authentication handshake failed"

Set `telemetry.otel.caPath` to the CA certificate path for the OTLP collector.
If the collector uses a publicly-trusted cert, ensure the container image
includes an up-to-date CA bundle. Setting `telemetry.otel.insecure: true` will
bypass TLS entirely (not recommended for production).

### Permission denied reading certificate files

The container runs as non-root user 65532 (see `podSecurityContext` in
values.yaml). Kubernetes secret volume mounts default to mode 0644, which is
readable by all users. If you override the secret volume `defaultMode`, ensure
the files remain readable by UID 65532.

---

## Full Example: All Transports with TLS

A single values file enabling TLS on every transport:

```yaml
tls:
  mode: server
  existingSecret: underpass-runtime-tls
  mountPath: /var/run/underpass-runtime/tls
  keys:
    cert: tls.crt
    key: tls.key
    clientCa: ca.crt

natsTls:
  mode: server
  existingSecret: underpass-runtime-nats-tls
  mountPath: /var/run/underpass-runtime/nats-tls
  keys:
    ca: ca.crt
    cert: ""
    key: ""

valkeyTls:
  enabled: true
  existingSecret: underpass-runtime-valkey-tls
  mountPath: /var/run/underpass-runtime/valkey-tls
  keys:
    ca: ca.crt
    cert: ""
    key: ""

eventBus:
  type: nats
  nats:
    url: tls://nats:4222

valkey:
  enabled: true
  host: valkey
  port: 6380
  existingSecret: valkey-password

stores:
  backend: valkey

artifacts:
  backend: s3
  s3:
    bucket: workspace-artifacts
    endpoint: minio:9000
    useSSL: true
    caPath: /var/run/underpass-runtime/tls/ca.crt
    existingSecret: minio-credentials

telemetry:
  backend: valkey
  otel:
    enabled: true
    endpoint: otel-collector:4317
    insecure: false
    caPath: /var/run/underpass-runtime/tls/ca.crt
```

```bash
helm upgrade --install underpass-runtime \
  charts/underpass-runtime \
  -f values-tls-full.yaml
```
