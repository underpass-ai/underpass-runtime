# E2E Tests — Underpass Runtime

End-to-end tests run as Kubernetes Jobs against a live runtime deployment.

## Test Catalog

| ID | Name | Tier | What it validates |
|----|------|------|-------------------|
| 01 | health | smoke | `/healthz` 200, `/metrics` 200, method-not-allowed 405 |
| 02 | session-lifecycle | smoke | Create, close, metadata, idempotent close, explicit ID, independence |
| 03 | tool-discovery | smoke | Compact/full detail, filters (risk, tags, scope, cost, side_effects) |
| 04 | recommendations | core | Heuristic scoring, task hint matching, top_k |
| 05 | invoke-basic | smoke | `fs.write_file`, `fs.read_file`, `fs.list` — basic tool invocations |
| 06 | invoke-policy | core | Policy enforcement, approval flows, risk-gated tools |
| 07 | invocation-retrieval | core | GET invocation by ID, logs, artifacts |
| 08 | data-flow | core | Full write → read → list → artifacts cycle |
| 10 | llm-agent-loop | full | LLM (Claude/OpenAI/vLLM) drives tool discovery + invocation loop |
| 11 | tool-learning-pipeline | core | DuckDB → Thompson Sampling → Valkey policies → NATS events |
| 12 | event-driven-agent | full | NATS event triggers code-review agent → workspace → findings → NATS |
| 13 | multi-agent-pipeline | full | 5-agent pipeline (architect → developer → test → review → QA) |
| 14 | full-infra-stack | full | TLS + Valkey persistence + NATS events + S3 artifacts end-to-end |
| 15 | vllm-learning-loop | full | vLLM agent → discovery → invoke → telemetry → recommendations adapt |
| 23 | runtime-rollout-tools | full | Specialist rollout tools (`get_replicasets`, `rollout_pause`, `rollout_undo`) against a live Deployment |
| 24 | runtime-saturation-notify-tools | full | Specialist saturation writes (`scale_deployment`, `restart_pods`, `circuit_break`) plus `notify.escalation_channel` |

## Running

```bash
# All tests
./e2e/run-e2e-tests.sh

# By tier
./e2e/run-e2e-tests.sh --tier smoke
./e2e/run-e2e-tests.sh --tier core

# Single test
./e2e/run-e2e-tests.sh --test 01

# Skip build/push (images already in registry)
./e2e/run-e2e-tests.sh --skip-build --skip-push --test 01
```

Requires `ghcr.io` authentication for image push and an `imagePullSecrets` named `ghcr-pull` in the namespace.

Test `24-runtime-saturation-notify-tools` also requires the runtime deployment to
set `WORKSPACE_NOTIFY_ESCALATION_ROUTES_JSON` for environment `e2e`. The
recommended route points at
`http://underpass-runtime-notify-sink.<namespace>.svc.cluster.local:8080/notify`;
the test creates that sink Service itself before invoking `notify.escalation_channel`.

## TLS Validation

Full TLS validation was performed on 2026-03-18 against a live K8s cluster with `tls.mode=server`.

### Setup

- Self-signed ECDSA CA (P-256) with `keyUsage: keyCertSign, cRLSign`
- Server cert with SAN: `DNS:underpass-runtime`, `DNS:underpass-runtime.underpass-runtime.svc.cluster.local`, `IP:127.0.0.1`
- TLS 1.3 minimum enforced by `internal/tlsutil`
- Helm deployed with `tls.mode=server`, `tls.existingSecret=runtime-tls`
- E2e jobs connect via `https://` with CA mounted at `/etc/ssl/runtime/ca.crt`

### Runtime Evidence

```
$ kubectl logs -l app.kubernetes.io/name=underpass-runtime

{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"HTTP server TLS configured","mode":"server"}
{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"session store initialized","backend":"memory"}
{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"artifact store initialized","backend":"local","root":"/tmp/artifacts"}
{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"invocation store initialized","backend":"memory"}
{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"event bus initialized","bus":"noop"}
{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"telemetry initialized","backend":"noop"}
{"time":"2026-03-18T18:58:57Z","level":"INFO","msg":"workspace service listening (TLS)","port":"50053","workspace_root":"/tmp/workspaces"}
```

### Helm Configuration

```
$ kubectl get configmap underpass-runtime -o yaml | grep TLS

WORKSPACE_TLS_MODE: server
WORKSPACE_TLS_CERT_PATH: /var/run/underpass-runtime/tls/tls.crt
WORKSPACE_TLS_KEY_PATH: /var/run/underpass-runtime/tls/tls.key
```

```
$ kubectl get svc underpass-runtime -o jsonpath='{.spec.ports[0]}'

name=https port=50053 targetPort=https
```

```
$ kubectl get deployment underpass-runtime -o jsonpath='{.spec.template.spec.containers[0].readinessProbe}'

{"httpGet":{"path":"/healthz","port":"https","scheme":"HTTPS"},"initialDelaySeconds":5,"periodSeconds":10}
```

### Test 1: Health Check via HTTPS

```
$ kubectl logs e2e-tls-health-9kz75

Step 1: GET /healthz returns 200 with status ok
OK GET /healthz -> 200, status=ok

Step 2: GET /metrics returns Prometheus metrics
OK GET /metrics -> 200

Step 3: POST /metrics returns 405 Method Not Allowed
OK POST /metrics -> 405 (Method Not Allowed)
OK All health tests passed
```

Evidence JSON:

```json
{
  "test_id": "01-health",
  "run_id": "e2e-health-1773860375",
  "status": "passed",
  "workspace_url": "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053",
  "steps": [
    {"step": "healthz", "status": "passed", "data": {"status": 200, "body": {"status": "ok"}}},
    {"step": "metrics", "status": "passed", "data": {"status": 200}},
    {"step": "metrics_method_not_allowed", "status": "passed", "data": {"status": 405}}
  ]
}
```

### Test 2: Session Lifecycle via HTTPS

```
$ kubectl logs e2e-tls-session-b95hv

Step 1: Create session and verify fields
OK Session session-6afe99f2d6faea61 created and closed

Step 2: Create session with metadata
OK Session with metadata created: session-f86b43bd667f5731

Step 3: Close nonexistent session is idempotent
OK Close nonexistent session returned 200

Step 4: GET /v1/sessions returns 405
OK GET /v1/sessions -> 405

Step 5: Multiple sessions are independent
OK Two independent sessions: session-fc4a7ad06110020f, session-6f4cb7d223d41707

Step 6: Double close same session
OK Double close returned 200 both times

Step 7: Create session with explicit ID
OK Explicit session ID honoured: e2e-explicit-1773860630
OK All session lifecycle tests passed
```

Evidence JSON:

```json
{
  "test_id": "02-session-lifecycle",
  "run_id": "e2e-session-1773860630",
  "status": "passed",
  "workspace_url": "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053",
  "steps": [
    {"step": "create_and_close", "status": "passed"},
    {"step": "metadata", "status": "passed", "data": {"session_id": "session-f86b43bd667f5731"}},
    {"step": "close_idempotent", "status": "passed"},
    {"step": "method_not_allowed", "status": "passed"},
    {"step": "multiple_independent", "status": "passed"},
    {"step": "double_close", "status": "passed"},
    {"step": "explicit_id", "status": "passed"}
  ],
  "sessions": [
    {"session_id": "session-6afe99f2d6faea61", "workspace_path": "/tmp/workspaces/e2e-tenant/session-6afe99f2d6faea61/repo"},
    {"session_id": "session-f86b43bd667f5731", "workspace_path": "/tmp/workspaces/e2e-tenant/session-f86b43bd667f5731/repo"},
    {"session_id": "session-fc4a7ad06110020f", "workspace_path": "/tmp/workspaces/e2e-tenant/session-fc4a7ad06110020f/repo"},
    {"session_id": "session-6f4cb7d223d41707", "workspace_path": "/tmp/workspaces/e2e-tenant/session-6f4cb7d223d41707/repo"},
    {"session_id": "session-1bef2a9e6133f018", "workspace_path": "/tmp/workspaces/e2e-tenant/session-1bef2a9e6133f018/repo"},
    {"session_id": "e2e-explicit-1773860630", "workspace_path": "/tmp/workspaces/e2e-tenant/e2e-explicit-1773860630/repo"}
  ]
}
```

### Test 3: LLM Agent Loop via HTTPS (OpenAI gpt-4o-mini)

Full agent loop: LLM creates a Go project through governed tool execution over TLS.

```
$ kubectl logs e2e-tls-llm-openai-b9h7t

Step 1: Creating workspace session
OK Session created: session-2dc582e508c77834

Step 2: Discovering tools (provider: openai)
OK Discovered 70 tools

Step 3: Getting tool recommendations
OK Got 10 recommendations

Step 4: Starting agent loop (max 10 iterations)
  --- Iteration 1/10 ---
  Thinking: I need to create a main.go file with a simple HTTP server
  Tool fs.write_file → succeeded
  --- Iteration 2/10 ---
  Thinking: Now I need to create a main_test.go file with a test
  Tool fs.write_file → succeeded
  --- Iteration 3/10 ---
  Thinking: Next, I will list the workspace to confirm both files exist
  Tool fs.list → succeeded
  --- Iteration 4/10 ---
  Thinking: Now I will read the content of main.go to verify
  Tool fs.read_file → succeeded
  --- Iteration 5/10 ---
  Agent done: Created a Go project with main.go and main_test.go

Step 5: Verifying workspace state
OK Workspace has 2 files (main.go: yes, test: yes)
OK LLM Agent Loop PASSED (provider=openai, iterations=5)
```

Evidence JSON:

```json
{
  "test_id": "10-llm-agent-loop",
  "run_id": "e2e-llm-openai-1773860822",
  "status": "pass",
  "workspace_url": "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053",
  "result": {
    "session_id": "session-2dc582e508c77834",
    "provider": "openai",
    "iterations": 5,
    "tools_discovered": 70,
    "recommendations": 10,
    "has_main": true,
    "has_test": true
  },
  "invocations": [
    {"tool": "fs.write_file", "invocation_id": "inv-b97bdbf7386e6e55", "invocation_status": "succeeded",
     "output": {"bytes_written": 216, "path": "main.go", "sha256": "75a678da58a4aa78157a4647150f8fdd5c3f99739a5ae3232d1360ec46f4c206"}},
    {"tool": "fs.write_file", "invocation_id": "inv-403ad9e8cbfb1731", "invocation_status": "succeeded",
     "output": {"bytes_written": 325, "path": "main_test.go", "sha256": "3a41bde0206784e4d4063ff6fd6f241561ea85797c83b80b92f33b51d35dc6b7"}},
    {"tool": "fs.list", "invocation_id": "inv-e94b65f8fa5850eb", "invocation_status": "succeeded",
     "output": {"count": 2, "entries": [{"path": "main.go", "size_bytes": 216}, {"path": "main_test.go", "size_bytes": 325}]}},
    {"tool": "fs.read_file", "invocation_id": "inv-5a8688d5e80117bb", "invocation_status": "succeeded",
     "output": {"content": "package main\n\nimport...", "path": "main.go", "size_bytes": 216}},
    {"tool": "fs.list", "invocation_id": "inv-bd7eae815d720651", "invocation_status": "succeeded",
     "output": {"count": 2}}
  ]
}
```

### Test 4: LLM Agent Loop via HTTPS (Claude sonnet-4)

TLS connection to runtime succeeded (session created, 70 tools discovered, 10 recommendations received). Failed at Anthropic API call (`HTTP 400`) — not a TLS issue.

```
$ kubectl logs e2e-tls-llm-claude-xjsk8

Step 1: Creating workspace session
OK Session created: session-7e79836db8d20031

Step 2: Discovering tools (provider: claude)
OK Discovered 70 tools

Step 3: Getting tool recommendations
OK Got 10 recommendations

Step 4: Starting agent loop (max 10 iterations)
  --- Iteration 1/10 ---
ERROR LLM Agent Loop FAILED: HTTP Error 400: Bad Request
```

Evidence: TLS to runtime worked (3 HTTPS calls succeeded). Failure was in the outbound Anthropic SDK call, not in the TLS transport.

### Summary

| Test | Transport | Provider | Steps | Status |
|------|-----------|----------|-------|--------|
| 01-health | HTTPS | - | 3/3 | **PASS** |
| 02-session-lifecycle | HTTPS | - | 7/7 | **PASS** |
| 10-llm-agent-loop | HTTPS | OpenAI gpt-4o-mini | 5 iterations, 5 invocations | **PASS** |
| 10-llm-agent-loop | HTTPS | Claude sonnet-4 | 3 HTTPS calls OK, Anthropic SDK 400 | **FAIL** (not TLS) |

All HTTPS connections verified against self-signed CA with TLS 1.3. The runtime correctly serves `ListenAndServeTLS` with cert/key from Kubernetes Secret, health probes use `scheme: HTTPS`, and service port is named `https`.
