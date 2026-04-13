# Runbook: TLS Certificate Rotation

Zero-downtime certificate renewal for all underpass-runtime transports.

---

## With cert-manager (Recommended)

If using cert-manager with the vLLM ingress or custom Certificate resources,
rotation is automatic. cert-manager renews certificates before expiry and
updates the K8s Secret. The runtime pod picks up new certs on restart.

```bash
# Check certificate status
kubectl -n underpass-runtime get certificate
kubectl -n underpass-runtime describe certificate <name>

# Force renewal
kubectl -n underpass-runtime delete secret <tls-secret-name>
# cert-manager will recreate it automatically
```

---

## Manual Rotation

### 1. Generate New Certificates

Follow the certificate generation steps in
[DEPLOYMENT-TLS.md](../operations/DEPLOYMENT-TLS.md#2-generate-self-signed-certificates).

Use the **same CA** unless you are rotating the CA itself.

### 2. Update Kubernetes Secrets

```bash
NS=underpass-runtime

# HTTP server TLS
kubectl -n $NS create secret generic underpass-runtime-tls \
  --from-file=tls.crt=new-tls.crt \
  --from-file=tls.key=new-tls.key \
  --from-file=ca.crt=ca.crt \
  --dry-run=client -o yaml | kubectl apply -f -

# NATS client TLS
kubectl -n $NS create secret generic underpass-runtime-nats-tls \
  --from-file=ca.crt=ca.crt \
  --from-file=tls.crt=new-client.crt \
  --from-file=tls.key=new-client.key \
  --dry-run=client -o yaml | kubectl apply -f -

# Valkey client TLS
kubectl -n $NS create secret generic underpass-runtime-valkey-tls \
  --from-file=ca.crt=ca.crt \
  --from-file=tls.crt=new-client.crt \
  --from-file=tls.key=new-client.key \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 3. Rolling Restart

Kubernetes mounts secrets as volumes. The runtime reads certs at startup.
Trigger a rolling restart to pick up new certs:

```bash
kubectl -n $NS rollout restart deployment/underpass-runtime
kubectl -n $NS rollout status deployment/underpass-runtime
```

With `PodDisruptionBudget` enabled, the restart maintains availability.

### 4. Verify

```bash
# Check TLS handshake
kubectl -n $NS run tls-check --rm -it --image=alpine/openssl -- \
  s_client -connect underpass-runtime:50053 -tls1_3 </dev/null 2>&1 | grep "Verify\|Protocol"

# Check certificate expiry
kubectl -n $NS run tls-check --rm -it --image=alpine/openssl -- \
  s_client -connect underpass-runtime:50053 </dev/null 2>&1 | \
  openssl x509 -noout -dates
```

---

## CA Rotation

Rotating the CA requires a two-phase approach to avoid downtime:

### Phase 1: Trust Both CAs

1. Generate new CA.
2. Create a combined CA bundle: `cat old-ca.crt new-ca.crt > combined-ca.crt`.
3. Update all secrets to use `combined-ca.crt` as the CA.
4. Rolling restart all components (runtime, Valkey, NATS).

### Phase 2: Switch to New CA Only

1. Issue new server/client certs signed by the new CA.
2. Update secrets with new certs and `new-ca.crt` (drop old CA).
3. Rolling restart all components.

---

## Rotation Schedule

| Certificate | Recommended Lifetime | Renewal Trigger |
|---|---|---|
| CA certificate | 3-5 years | 6 months before expiry |
| Server certificate | 1 year | 30 days before expiry |
| Client certificate | 1 year | 30 days before expiry |

Set calendar reminders or use cert-manager's automatic renewal.
