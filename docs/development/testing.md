# Testing

This document describes the test strategy, test matrix, and how to run tests
for underpass-runtime.

---

## Test Pyramid

```
         ╱ E2E Tests (14 scenarios) ╲
        ╱   K8s Jobs against live    ╲
       ╱    cluster with TLS          ╲
      ╱─────────────────────────────────╲
     ╱ Integration Tests                 ╲
    ╱   devcontainer + Valkey + NATS      ╲
   ╱───────────────────────────────────────╲
  ╱ Unit Tests                              ╲
 ╱   Hand-written fakes, no infra required   ╲
╱─────────────────────────────────────────────╲
```

---

## Unit Tests

### Coverage Gates

| Scope | Minimum | Packages | Command |
|---|---|---|---|
| **Core gate** | 80% | `./internal/app`, `./internal/adapters/audit`, `./internal/adapters/policy`, `./internal/adapters/sessionstore`, `./internal/adapters/invocationstore` | `make coverage-core` |
| **Full report** | 70% (SonarCloud) | `./...` | `make coverage-full` |

### Test Matrix by Package

| Package | Test Files | Focus | Fake Dependencies |
|---|---|---|---|
| `internal/app` | `service_unit_test.go`, `service_discovery_test.go`, `service_recommendation_test.go`, `service_invocation_test.go`, `service_session_test.go`, `service_correlation_test.go`, `context_digest_test.go`, `kpi_metrics_test.go`, `recommender_test.go` | Business logic, session lifecycle, tool invocation flow, discovery filters, recommendations, correlation, KPI metrics | `fakeWorkspaceManager`, `fakeCatalog`, `fakeToolEngine`, `fakeSessionStore`, `fakeInvocationStore`, `fakeArtifactStore`, `fakeAuditLogger`, `fakeEventPublisher` |
| `internal/adapters/policy` | `static_policy_test.go`, `static_policy_extra_test.go`, `rbac_policy_test.go` | Role-based access, path validation, risk gating, approval flow, rate limiting, namespace/registry allow-lists | None (pure logic) |
| `internal/adapters/tools` | 40+ test files (`*_test.go` per tool family) | Tool argument validation, output schema conformance, error handling | `fakeCommandRunner`, `fakeWorkspaceManager` |
| `internal/adapters/workspace` | `local_manager_test.go`, `docker_manager_test.go`, `kubernetes_manager_test.go`, `janitor_test.go` | Session creation/cleanup, container lifecycle, pod lifecycle, janitor GC | k8sfake (for K8s), Docker client mock |
| `internal/adapters/sessionstore` | `valkey_store_test.go` | Valkey CRUD, TTL, key prefix | miniredis |
| `internal/adapters/invocationstore` | `valkey_store_test.go` | Valkey CRUD, TTL, key prefix | miniredis |
| `internal/adapters/audit` | `logger_audit_test.go` | Sensitive data redaction, event recording | None (captures slog output) |
| `internal/adapters/eventbus` | `nats_publisher_test.go`, `outbox_relay_test.go` | NATS publish, outbox pattern, relay | Embedded NATS server, miniredis |
| `internal/adapters/storage` | `s3_store_test.go`, `local_store_test.go` | S3 CRUD, local file store | MinIO test container / temp dir |
| `internal/adapters/telemetry` | `valkey_recorder_test.go`, `memory_recorder_test.go` | Telemetry recording, aggregation | miniredis / in-memory |
| `internal/httpapi` | `server_test.go`, `auth_test.go` | HTTP routing, status codes, auth middleware | fakeService |
| `internal/tlsutil` | `tls_test.go` | TLS config construction, cert loading, mode parsing | temp certs |
| `cmd/workspace` | `main_test.go`, `main_k8s_test.go`, `main_nok8s_test.go` | Bootstrap integration, env var parsing, graceful shutdown | Embedded fakes |

### Testing Philosophy

- **No mocking frameworks**: All test doubles are hand-written fakes
  implementing the same port interfaces defined in `internal/app/types.go`.
  This ensures type safety at compile time and avoids mock-specific assertion
  libraries.

- **Table-driven tests**: Used extensively. Test cases are defined as struct
  slices with descriptive names, inputs, and expected outputs.

- **Race detector**: All CI test runs include `-race` to catch data races.

- **Build-tag matrix**: Tests run in two variants — default (docker-local)
  and `-tags k8s` — to cover both build configurations.

### Running Unit Tests

```bash
# All tests with race detector
make test

# Core coverage gate (80% minimum)
make coverage-core

# Full coverage report (for SonarCloud)
make coverage-full

# Single package
go test -v ./internal/adapters/policy/...

# With k8s build tag
go test -race -count=1 -tags k8s ./...
```

---

## Integration Tests

Integration tests run inside a devcontainer with live Valkey and NATS
instances.

### Prerequisites

- Docker or Podman
- Docker Compose

### Running

```bash
# Start infrastructure and run tests
make integration-test

# Or manage lifecycle separately
make integration-up
# ... run tests manually ...
make integration-down
```

### What They Cover

- Valkey-backed session/invocation stores with real Redis protocol
- NATS event publishing with JetStream
- Outbox relay with real Valkey + NATS
- TLS connections to Valkey and NATS

---

## E2E Tests

End-to-end tests run as Kubernetes Jobs against a live runtime deployment.
They validate the full stack: TLS, auth, session lifecycle, tool execution,
event flow, and LLM integration.

### Test Catalog

| ID | Name | Tier | Type | Timeout | What It Validates |
|---|---|---|---|---|---|
| 01 | health | smoke | workspace | 300s | `/healthz` 200, `/metrics` 200, 405 on wrong method |
| 02 | session-lifecycle | smoke | workspace | 600s | Create, close, metadata, idempotent close, explicit ID, independence |
| 03 | tool-discovery | smoke | workspace | 600s | Compact/full detail, filters (risk, tags, scope, cost, side_effects) |
| 04 | recommendations | core | workspace | 600s | Heuristic scoring, task hint matching, top_k |
| 05 | invoke-basic | smoke | workspace | 600s | `fs.write_file`, `fs.read_file`, `fs.list` — basic tool invocations |
| 06 | invoke-policy | core | workspace | 600s | Policy enforcement, approval flows, risk-gated tools |
| 07 | invocation-retrieval | core | workspace | 600s | GET invocation by ID, logs, artifacts |
| 08 | data-flow | core | workspace | 900s | Full write → read → list → artifacts cycle |
| 10 | llm-agent-loop | full | llm | 600s | LLM (Claude/OpenAI/vLLM) drives tool discovery + invocation loop |
| 11 | tool-learning-pipeline | core | tool-learning | 600s | DuckDB → Thompson Sampling → Valkey → NATS |
| 12 | event-driven-agent | full | agent | 300s | NATS event → agent activates → workspace → NATS |
| 13 | multi-agent-pipeline | full | agent | 300s | 5-agent collaborative pipeline |
| 14 | full-infra-stack | full | infra | 120s | TLS + Valkey + NATS + S3 end-to-end |

### Tiers

| Tier | Tests | Purpose | When to Run |
|---|---|---|---|
| **smoke** | 01, 02, 03, 05 | Basic health and functionality | Every deployment |
| **core** | 04, 06, 07, 08, 11 | Policy, retrieval, data flow, tool learning | Every PR |
| **full** | 10, 12, 13, 14 | LLM integration, multi-agent, full infra | Before release |

### Running

```bash
# All tiers
./e2e/run-e2e-tests.sh

# By tier
./e2e/run-e2e-tests.sh --tier smoke
./e2e/run-e2e-tests.sh --tier core

# Single test
./e2e/run-e2e-tests.sh --test 05

# Skip build/push (images already in registry)
./e2e/run-e2e-tests.sh --skip-build --skip-push --tier smoke

# Custom namespace
./e2e/run-e2e-tests.sh --namespace my-namespace --test 01
```

### Evidence Collection

Each test produces a structured JSON evidence file:

```json
{
  "test_id": "05-invoke-basic",
  "run_id": "e2e-invoke-1773860400",
  "status": "passed",
  "workspace_url": "https://underpass-runtime:50053",
  "steps": [...],
  "invocations": [...],
  "sessions": [...]
}
```

Evidence files are extracted from job pods and stored in `e2e/evidence/`.

### TLS in E2E

All E2E tests support HTTPS endpoints. TLS validation was performed with:
- Self-signed ECDSA CA (P-256)
- TLS 1.3 minimum enforced
- Server and mutual TLS modes tested
- See `e2e/README.md` for full TLS validation evidence.

---

## CI Test Pipeline

```
┌─────────┐    ┌───────────────────┐    ┌───────────────────┐
│  Lint   │    │ Test (docker-local)│    │    Test (k8s)     │
│ golangci│    │ -race -count=1    │    │ -race -tags k8s   │
│ go vet  │    │ 80% core gate     │    │ coverage report   │
└────┬────┘    └────────┬──────────┘    └────────┬──────────┘
     │                  │                        │
     │           ┌──────▼──────────────────────  │
     │           │ SonarCloud (merged coverage)  │◄─┘
     │           │ 70% overall, 80% new code     │
     │           └───────────────────────────────┘
     │
     ├──────────▶ Security (govulncheck)
     ├──────────▶ CodeQL (SAST)
     │
     └──────────▶ Docker build (gated on lint + test + build)
```

### Quality Gates

| Gate | Threshold | Enforced By |
|---|---|---|
| Core test coverage | >= 80% | CI (`coverage-core.out`) |
| SonarCloud overall coverage | >= 70% | SonarCloud quality gate |
| SonarCloud new code coverage | >= 80% | SonarCloud quality gate |
| Lint (golangci-lint) | Zero issues | CI (fail on error) |
| Race detector | Zero races | CI (`-race` flag) |
| Build (2 variants) | Zero errors | CI matrix (docker-local + k8s) |

---

## Tool-Learning Service Tests

The `services/tool-learning/` service has its own test suite:

```bash
cd services/tool-learning

# Unit tests
make test

# Coverage gate (80% on core packages)
make coverage-core

# Integration tests (requires Docker for DuckDB + miniredis)
make integration-test
```

### Decoupling Guard

CI enforces that tool-learning does **not** import the workspace Go module.
This prevents tight coupling between services. See
`.github/workflows/ci-tool-learning.yml` (decoupling-guard job).
