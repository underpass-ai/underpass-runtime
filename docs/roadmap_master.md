# Roadmap Master

Quality roadmap for underpass-runtime. Tracks gaps found during the
comparative audit against rehydration-kernel, plus code-vs-documentation
discrepancies.

Last updated: 2026-03-31

---

## Status Legend

| Symbol | Meaning |
|--------|---------|
| done | Completed in `chore/quality-audit` (PR #41) |
| next | Ready to implement, high impact |
| planned | Scheduled, medium priority |
| backlog | Low priority or structural |

---

## 1. Documentation Gaps (Code vs Docs)

### 1.1 README Discrepancies

| # | Gap | Severity | Status | Details |
|---|-----|----------|--------|---------|
| D1 | **Tool count wrong** (says 96, actual 99) | HIGH | done | Fixed in PR #41 — README now says 99 |
| D2 | **Tool families understated** (says 15+, actual 23) | MEDIUM | done | README table now lists all 23 families with counts |
| D3 | **2 API endpoints missing from README** | CRITICAL | done | discovery + recommendations added in PR #41 |

### 1.2 E2E Test Documentation

| # | Gap | Severity | Status | Details |
|---|-----|----------|--------|---------|
| D4 | **3 E2E tests undocumented** in `e2e/README.md` | MEDIUM | done | Tests 12, 13, 14, 15 all documented in e2e/README.md |

---

## 2. Audit Gaps Closed (PR #41)

### 2.1 Documentation (11 new files)

| # | Item | Status | File |
|---|------|--------|------|
| A1 | Security model with threat model | done | `docs/security-model.md` |
| A2 | ADR-001 Hexagonal Architecture | done | `docs/adr/ADR-001-hexagonal-architecture-in-go.md` |
| A3 | ADR-002 YAML Tool Catalog | done | `docs/adr/ADR-002-yaml-tool-catalog.md` |
| A4 | ADR-003 Thompson Sampling | done | `docs/adr/ADR-003-thompson-sampling-tool-recommendations.md` |
| A5 | Testing guide with matrices | done | `docs/testing.md` |
| A6 | Kubernetes deployment guide | done | `docs/operations/kubernetes-deploy.md` |
| A7 | Cluster prerequisites | done | `docs/operations/cluster-prerequisites.md` |
| A8 | Incident response runbook | done | `docs/runbooks/incident-response.md` |
| A9 | Scaling runbook | done | `docs/runbooks/scaling.md` |
| A10 | TLS rotation runbook | done | `docs/runbooks/tls-rotation.md` |
| A11 | Documentation index | done | `docs/README.md` |

### 2.2 CI & Infrastructure (5 new files, 2 modified)

| # | Item | Status | File |
|---|------|--------|------|
| A12 | `.dockerignore` | done | `.dockerignore` |
| A13 | Dependabot config | done | `.github/dependabot.yml` |
| A14 | SonarCloud blocking gate | done | `sonar-project.properties` |
| A15 | Local quality gate script | done | `scripts/ci/quality-gate.sh` |
| A16 | `make quality-gate` target | done | `Makefile` |

### 2.3 Helm Multi-Values (3 new files)

| # | Item | Status | File |
|---|------|--------|------|
| A17 | Development overrides | done | `charts/.../values.dev.yaml` |
| A18 | Production overrides | done | `charts/.../values.production.yaml` |
| A19 | mTLS example | done | `charts/.../values.mtls.example.yaml` |

### 2.4 Code Quality (6 modified files)

| # | Item | Status | File |
|---|------|--------|------|
| A20 | panic→error in catalog loader | done | `internal/adapters/tools/catalog_defaults.go` |
| A21 | Godoc on NewService | done | `internal/app/service.go` |
| A22 | Godoc on NewInMemorySessionStore | done | `internal/app/session_store_memory.go` |
| A23 | Godoc on NewInMemoryInvocationStore | done | `internal/app/invocation_store_memory.go` |
| A24 | vLLM server securityContext | done | `e2e/tests/10-llm-agent-loop/vllm-server.yaml` |
| A25 | CONTRIBUTING.md refresh | done | `CONTRIBUTING.md` |
| A26 | SECURITY.md refresh | done | `SECURITY.md` |

---

## 3. Next Actions (Not Yet Started)

### 3.1 Documentation Fixes (Priority: next)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N1 | Fix README tool count: 96 → 99 | 5 min | done |
| N2 | Fix README family count: 15+ → 23 | 5 min | done |
| N3 | Add discovery + recommendations endpoints to README API table | 10 min | done (already present after PR #41 merge) |
| N4 | Add tests 12, 13, 14 to `e2e/README.md` test catalog | 15 min | done |

### 3.2 Code Quality (Priority: planned)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N5 | Remove unused constants in `fs_tools.go` | 10 min | done |
| N6 | Remove unused `boolPtr` in `container_tools.go` | 5 min | not-applicable (used in k8s build tag) |
| N7 | Remove unused `containerMaxContainerNameSize` in `container_tools.go` | 5 min | done |
| N8 | Remove unused `capabilityFamily` in `catalog_docs.go` | 5 min | not-applicable (used in catalog_docs.go) |
| N9 | Fix `dependency_tools.go:532` — replace if with `strings.TrimPrefix` (S1017) | 5 min | done |
| N10 | Add bootstrap package tests (`data.go`, `messaging.go`, `secops.go`, `docker.go`) | 2-4 hours | Coverage |

### 3.3 CI Improvements (Priority: planned)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N11 | Fix SonarCloud token | 15 min | done (SONAR_TOKEN secret updated) |
| N12 | Add Helm chart linting job to CI (`helm lint charts/underpass-runtime`) | 30 min | done |
| N13 | Add pre-commit hooks (`.pre-commit-config.yaml`: gofmt, govet, lint) | 30 min | done |

### 3.4 Observability (Priority: planned)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N14 | Domain-layer observability value objects (like kernel's `BundleQualityMetrics`) | 4-8 hours | done (InvocationQualityMetrics + QualityObserver port) |
| N15 | Document OTel instruments inventory (current metrics list) | 1 hour | done (docs/observability.md) |

### 3.5 Contract Management (Priority: backlog)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N16 | Create OpenAPI spec for HTTP API | 4-8 hours | done |
| N17 | Add contract validation gate in CI | 2-4 hours | done |
| N18 | AsyncAPI spec for NATS domain events | 2-4 hours | done |

### 3.6 Release & Versioning (Priority: backlog)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N19 | Semantic versioning strategy + git tags | 1 hour | done (release.yml) |
| N20 | CHANGELOG.md (conventional-commits based) | 1 hour | done (auto-generated in release.yml) |
| N21 | Release automation (GitHub Actions) | 2-4 hours | done |
| N22 | Helm chart version automation | 1-2 hours | backlog |

### 3.7 Structural (Priority: backlog)

| # | Action | Effort | Impact |
|---|--------|--------|--------|
| N23 | Split `fs_tools.go` (1624 lines) into per-operation files | 2-4 hours | Maintainability |
| N24 | Standardize error wrapping (always `fmt.Errorf %w`) | 2-4 hours | Error chain quality |
| N25 | Separate `CorrelationFinder` interface from `InvocationStore` | 30 min | done (added godoc, already separate) |

---

## 4. Comparison Summary vs Rehydration-Kernel

| Dimension | Kernel | Runtime (before) | Runtime (after PR #41) | Gap |
|-----------|--------|-------------------|------------------------|-----|
| Security model doc | Comprehensive | 22 lines | Comprehensive | Closed |
| ADRs | Yes (ADR-007+) | None | 3 ADRs | Closed |
| Operations guides | 5 docs | Fragments | 2 structured docs | Closed |
| Runbooks | Yes | None | 3 runbooks | Closed |
| Test documentation | 528 lines | Basic | 253 lines | Closed |
| .dockerignore | Yes | Missing | Yes | Closed |
| Helm multi-values | 5 files | 1 file | 4 files | Closed |
| SonarCloud blocking | Yes | No | Yes | Closed |
| Dependabot | Yes | No | Yes | Closed |
| Quality gate script | Yes | No | Yes | Closed |
| Contract validation | buf + contract-gate | None | OpenAPI + Helm lint in CI | Closed |
| OpenAPI/AsyncAPI specs | AsyncAPI | None | OpenAPI 3.1 + AsyncAPI 3.0 | Closed |
| Release automation | Manual | Manual | release.yml (tag → build → GH release → GHCR) | Closed |
| CHANGELOG | None | None | Auto-generated in release workflow | Closed |
| Pre-commit hooks | Compensated | None | .pre-commit-config.yaml | Closed |
| Domain observability | Value objects | Basic metrics | docs/observability.md + domain-driven metrics | Closed |

---

## 8. RTK Inspiration Notes

These notes capture RTK ideas that are worth reusing in `underpass-runtime`:

- [RTK gap analysis](../rehydration-kernel/docs/research/rtk-useful-gap-analysis.md)
- [RTK ideas for Underpass Runtime](../rehydration-kernel/docs/research/rtk-runtime-ideas.md)
- [RTK command coverage gaps](../rehydration-kernel/docs/research/rtk-missing-tools.md)
- [RTK-inspired runtime roadmap](../rehydration-kernel/docs/research/underpass-runtime-rtk-roadmap.md)

The goal is not to copy RTK’s shell proxy architecture.
The goal is to reuse the UX lessons: transparent activation, family-based routing,
output shaping by tool type, safe fallback, and visible savings.
