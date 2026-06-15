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

### Per-tool matrix

The suite is a single data-driven runner (`e2e/tests/00-tool-matrix`) that loads
the tool catalog and exercises every registered tool (~130). See
[`e2e/README.md`](../../e2e/README.md) for the full design. Per tool it derives:

| Case | Applies to | Asserts |
|---|---|---|
| `discovery` | all | tool registered + visible in the session catalog |
| `happy_path` | tools with an `example` | invoke with the catalog's own example args |
| `invalid_input` | tools with required fields | empty args are rejected, never executed |
| `approval_gate` | `requires_approval` tools | invoking without approval is blocked |
| `policy_traversal` | tools with a `path_field` | a workspace-escape path is denied |

The three governance cases are the deterministic backbone; `happy_path` records
real `succeeded` execution for workspace-local tools, `fail` if a tool's own
example is rejected by governance, and `executed` (non-failing) when an external
dependency is unavailable.

### Running

```bash
# Whole matrix (build + push + deploy the Job)
./e2e/run-e2e-tests.sh --test 00

# Skip build/push (image already in registry)
./e2e/run-e2e-tests.sh --skip-build --skip-push --test 00

# Local dry-run of the case matrix (no cluster)
E2E_DRY_RUN=1 CATALOG_PATH=internal/adapters/tools/catalog_defaults.yaml \
  python3 e2e/tests/00-tool-matrix/test_tool_matrix.py
```

### Evidence Collection

The runner emits a structured JSON evidence file with every `{tool, case,
outcome, detail}` plus a roll-up:

```json
{
  "test_id": "00-tool-matrix",
  "status": "passed",
  "summary": {"tools": 130, "cases": 387, "by_outcome": {"pass": 0, "executed": 0, "fail": 0}},
  "cases": [{"tool": "fs.write_file", "case": "happy_path", "outcome": "pass"}]
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
