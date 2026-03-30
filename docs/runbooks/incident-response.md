# Runbook: Incident Response

## Alerts That Trigger This Runbook

- `WorkspaceDown` (critical)
- `WorkspaceInvocationFailureRateHigh` (warning)
- `WorkspaceInvocationDeniedRateHigh` (warning)
- `WorkspaceP95InvocationLatencyHigh` (warning)

---

## Triage

### 1. Check Pod Health

```bash
NS=underpass-runtime

# Pod status
kubectl -n $NS get pods -l app.kubernetes.io/name=underpass-runtime

# Recent events
kubectl -n $NS describe deployment underpass-runtime

# Container restarts
kubectl -n $NS get pods -l app.kubernetes.io/name=underpass-runtime \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.containerStatuses[0].restartCount}{"\n"}{end}'
```

### 2. Check Logs

```bash
# Recent logs (last 5 minutes)
kubectl -n $NS logs -l app.kubernetes.io/name=underpass-runtime --since=5m

# Filter for errors
kubectl -n $NS logs -l app.kubernetes.io/name=underpass-runtime --since=5m | grep -i error

# Previous container (if restarted)
kubectl -n $NS logs -l app.kubernetes.io/name=underpass-runtime --previous
```

### 3. Check Dependencies

```bash
# Valkey
kubectl -n $NS exec deploy/underpass-runtime -- \
  wget -q -O- http://valkey-master:6379/ping 2>/dev/null || echo "Valkey unreachable"

# NATS
kubectl -n $NS get pods -l app.kubernetes.io/name=nats

# MinIO/S3
kubectl -n $NS get pods -l app=minio
```

### 4. Check Metrics

```bash
# Port-forward to runtime
kubectl -n $NS port-forward svc/underpass-runtime 50053:50053 &

# Invocation metrics
curl -s http://localhost:50053/metrics | grep workspace_invocations_total

# Latency histogram
curl -s http://localhost:50053/metrics | grep workspace_duration_ms

# Denied requests
curl -s http://localhost:50053/metrics | grep workspace_denied_total
```

---

## Common Issues

### WorkspaceDown — Pod CrashLoopBackOff

**Cause**: Missing TLS secrets, Valkey unreachable, invalid configuration.

```bash
# Check events
kubectl -n $NS describe pod -l app.kubernetes.io/name=underpass-runtime

# Common: "tls.existingSecret is required when tls.mode is server or mutual"
# Fix: create the missing K8s secret or set tls.mode=disabled
```

### High Failure Rate

**Cause**: Backend infrastructure (Valkey, NATS, S3) degraded or workspace
pods failing.

```bash
# Check which tools are failing
curl -s http://localhost:50053/metrics | grep 'workspace_invocations_total.*status="failed"'

# Check workspace pod status (K8s backend)
kubectl -n $NS get pods -l underpass.ai/type=workspace
```

### High Latency

**Cause**: Valkey slow, S3 latency, workspace pod scheduling delays.

```bash
# P95 latency by tool
curl -s http://localhost:50053/metrics | grep workspace_duration_ms_bucket

# Check Valkey latency
kubectl -n $NS exec -it deploy/valkey-master -- valkey-cli --latency
```

### High Denied Rate

**Cause**: Agents requesting tools without proper roles or approval.

```bash
# Check denial reasons
curl -s http://localhost:50053/metrics | grep 'workspace_denied_total'

# Check recent audit logs for denied invocations
kubectl -n $NS logs -l app.kubernetes.io/name=underpass-runtime --since=10m | grep "denied"
```

---

## Escalation

| Severity | Response Time | Contact |
|---|---|---|
| Critical (WorkspaceDown) | 15 minutes | On-call engineer |
| Warning (rates, latency) | 1 hour | Platform team |
| Security incident | Immediate | security@underpass.ai |
