# SonarCloud Issue Remediation Plan

> **Project**: underpass-ai/underpass-runtime
> **Dashboard**: https://sonarcloud.io/project/issues?issueStatuses=OPEN%2CCONFIRMED&id=underpass-ai_underpass-runtime
> **Date**: 2026-03-07
> **Total Issues**: 783 (all MAINTAINABILITY)

## Summary

| Severity | Count | Rules |
|----------|------:|-------|
| CRITICAL | 200   | S1192 (180), S3776 (19), S1186 (1) |
| MINOR    | 583   | S100 (477), S8193 (104), S7031 (2) |

All issues are maintainability — **zero security or reliability issues**.

## Rules Breakdown

| Rule | Count | Severity | Description | Fix Strategy |
|------|------:|----------|-------------|--------------|
| `go:S100` | 477 | MINOR | Test function names with underscores (`Test_Foo_Bar`) | **Bulk rename** or **suppress** — Go convention uses underscores in tests |
| `go:S1192` | 180 | CRITICAL | Duplicated string literals → extract constants | **Extract test constants** per file |
| `godre:S8193` | 104 | MINOR | Unnecessary `err :=` declarations, use `if err := ...; err != nil` | **Inline error checks** |
| `go:S3776` | 19 | CRITICAL | Cognitive complexity exceeds 15 | **Refactor** complex test/production functions |
| `docker:S7031` | 2 | MINOR | Consecutive RUN instructions in Dockerfile | **Merge RUN** statements |
| `go:S1186` | 1 | CRITICAL | Empty function body without explanation | **Add comment** or implement |

---

## Phased Remediation Plan

### Phase 1 — Quick Wins (est. ~50 issues, 1 session)

**Goal**: Fix all CRITICAL issues that are trivial and reduce noise.

#### 1.1 — `go:S1186` Empty function (1 issue)
- `internal/app/service_unit_test.go:139` — add explanatory comment

#### 1.2 — `docker:S7031` Consecutive RUN (2 issues)
- `e2e-images/15-workspace-vllm-tool-orchestration.Dockerfile` — merge RUN instructions

#### 1.3 — `go:S3776` Cognitive complexity in production code (1 issue)
- `internal/adapters/tools/connection_tools.go:232` (complexity 16, threshold 15)

**Phase 1 total**: 4 issues fixed

---

### Phase 2 — Suppress S100 Rule (477 issues, 1 session)

**Rationale**: Go's standard testing convention uses underscores in test function names (`TestFoo_SubScenario`). SonarCloud's `go:S100` regex `^(_|[a-zA-Z0-9]+)$` rejects this. The idiomatic Go approach is correct.

**Action**: Add rule exclusion in `sonar-project.properties`:
```properties
sonar.issue.ignore.multicriteria=e1
sonar.issue.ignore.multicriteria.e1.ruleKey=go:S100
sonar.issue.ignore.multicriteria.e1.resourceKey=**/*_test.go
```

This is NOT suppressing real issues — it's aligning SonarCloud with Go conventions.

**Phase 2 total**: 477 issues resolved

---

### Phase 3 — Duplicated String Literals (180 issues, 2–3 sessions)

Extract repeated test literals into file-level `const` blocks. Prioritize by count.

#### Batch A — High-count files (8 files, ~78 issues)

| File | Issues |
|------|-------:|
| `internal/adapters/tools/image_tools_test.go` | 14 |
| `internal/adapters/tools/container_tools_test.go` | 12 |
| `internal/httpapi/server_test.go` | 11 |
| `internal/adapters/tools/ci_pipeline_tools_test.go` | 8 |
| `internal/adapters/tools/container_scan_tools_test.go` | 8 |
| `internal/httpapi/auth_test.go` | 8 |
| `internal/adapters/tools/connection_tools_resolve_test.go` | 7 |
| `internal/adapters/tools/fs_tools_test.go` | 7 |

Common patterns:
- `"workspace:test"` → `const testWorkspaceID = "workspace:test"`
- `"session-1"` → `const testSessionID = "session-1"`
- `"test-tool"` → `const testToolName = "test-tool"`
- Tool names, error messages, JSON keys

#### Batch B — Medium-count files (12 files, ~64 issues)

| File | Issues |
|------|-------:|
| `internal/adapters/tools/dependency_tools_test.go` | 6 |
| `internal/adapters/tools/language_tool_handlers_test.go` | 6 |
| `internal/adapters/tools/license_tools_test.go` | 6 |
| `internal/app/service_unit_test.go` | 6 |
| `internal/adapters/tools/build_tools_test.go` | 5 |
| `internal/adapters/workspace/kubernetes_manager_test.go` | 5 |
| `internal/adapters/invocationstore/valkey_store_test.go` | 4 |
| `internal/adapters/policy/static_policy_test.go` | 4 |
| `internal/adapters/tools/kafka_tools_test.go` | 4 |
| `internal/adapters/tools/nats_tools_test.go` | 4 |
| `internal/adapters/tools/rabbit_tools_test.go` | 4 |
| `internal/adapters/tools/redis_tools_test.go` | 4 |

#### Batch C — Low-count files (21 files, ~38 issues)
All remaining files with 1–3 duplicated literals each.

**Phase 3 total**: 180 issues fixed

---

### Phase 4 — Inline Error Declarations S8193 (104 issues, 2 sessions)

Replace `err := foo(); if err != nil` with `if err := foo(); err != nil` where the variable is only used in the condition.

**IMPORTANT**: Some cases CANNOT be inlined (e.g., `service_unit_test.go:245` where `err` is reused later). Review each before changing.

#### Batch A — Production code (14 files, ~79 issues)

| File | Issues |
|------|-------:|
| `internal/adapters/tools/language_tool_handlers.go` | 14 |
| `internal/adapters/tools/git_tools.go` | 11 |
| `internal/adapters/tools/fs_tools.go` | 10 |
| `internal/adapters/policy/static_policy.go` | 9 |
| `internal/adapters/tools/redis_tools.go` | 7 |
| `internal/adapters/tools/toolchain_tools.go` | 6 |
| `internal/adapters/tools/repo_analysis_tools.go` | 5 |
| `internal/adapters/tools/connection_tools.go` | 4 |
| `internal/adapters/tools/container_tools.go` | 4 |
| `internal/adapters/tools/image_tools.go` | 4 |
| `internal/adapters/tools/k8s_delivery_tools.go` | 4 |
| `internal/adapters/tools/kafka_tools.go` | 3 |
| `internal/adapters/tools/nats_tools.go` | 3 |
| `internal/adapters/tools/rabbit_tools.go` | 3 |

#### Batch B — Test code & misc (11 files, ~25 issues)

| File | Issues |
|------|-------:|
| `internal/adapters/tools/image_tools_test.go` | 3 |
| `internal/adapters/tools/repo_tools.go` | 3 |
| `internal/adapters/tools/mongo_tools.go` | 2 |
| `internal/httpapi/server.go` | 2 |
| Other files (7 files × 1 issue each) | 7 |

**Phase 4 total**: 104 issues fixed

---

### Phase 5 — Cognitive Complexity S3776 (19 issues, 1–2 sessions)

Refactor functions exceeding cognitive complexity threshold of 15.

#### 5.1 — Test code (18 issues)

Most test functions exceed complexity because of large table-driven test loops with nested assertions. Strategy: extract assertion helpers or split into sub-tests.

| File | Line | Complexity | Strategy |
|------|-----:|----------:|----------|
| `catalog_factories_test.go` | 15 | 56 | Split into per-category sub-tests |
| `git_tools_test.go` | 139 | 34 | Extract validation helper |
| `api_benchmark_tools_test.go` | 29 | 23 | Extract assertion helper |
| `static_policy_test.go` | 76 | 20 | Sub-test extraction |
| `static_policy_test.go` | 242 | 20 | Sub-test extraction |
| `license_tools_test.go` | 15 | 20 | Sub-test extraction |
| `kafka_tools_test.go` | 307 | 20 | Extract validation helper |
| `container_tools_test.go` | 192 | 18 | Sub-test extraction |
| `image_tools_test.go` | 298 | 17 | Sub-test extraction |
| `image_tools_test.go` | 385 | 17 | Sub-test extraction |
| `kafka_tools_test.go` | 204 | 17 | Extract validation helper |
| `catalog_defaults_test.go` | 5 | 17 | Split assertions |
| `container_scan_tools_test.go` | 16 | 16 | Sub-test extraction |
| `container_scan_tools_test.go` | 74 | 16 | Sub-test extraction |
| `image_tools_test.go` | 195 | 16 | Sub-test extraction |
| `kafka_tools_test.go` | 370 | 16 | Sub-test extraction |
| `license_tools_test.go` | 110 | 16 | Sub-test extraction |
| `kubernetes_manager_test.go` | 89 | 16 | Sub-test extraction |

#### 5.2 — Production code (1 issue)
- `connection_tools.go:232` (complexity 16) — already in Phase 1

**Phase 5 total**: 19 issues fixed

---

## Execution Order & Priority

| Phase | Issues | Priority | Sessions | Cumulative |
|-------|-------:|----------|----------|----------:|
| 1 — Quick Wins | 4 | P0 | 1 | 4 |
| 2 — Suppress S100 | 477 | P0 | 1 | 481 |
| 3 — String Constants | 180 | P1 | 2–3 | 661 |
| 4 — Inline Errors | 104 | P2 | 2 | 765 |
| 5 — Complexity | 19 | P2 | 1–2 | 783* |

*Note: Phase 1.3 overlaps with Phase 5.2 (1 issue), so the deduplicated total is 783.

## Target State

After all phases:
- **0 open issues** on SonarCloud
- **Quality Gate**: PASSED
- New code coverage minimum: 80%
- Overall coverage minimum: 70%

## Rules

1. **Never modify SonarCloud config to hide real issues** — fix the code
2. S100 suppression in tests is acceptable because Go convention explicitly uses underscores in test names
3. All changes must pass existing tests — run `go test ./...` after each batch
4. Commit per batch, not per file — keeps history clean
