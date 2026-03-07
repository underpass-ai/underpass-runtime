# SonarCloud Remediation Plan — underpass-runtime

**Date:** 2026-03-07
**Branch:** `feat/k8s-build-tags` (PR #1)
**Total issues:** 783

## Executive Summary

| Rule | Severity | Count | Category | Fix Strategy |
|------|----------|-------|----------|-------------|
| `go:S100` | MINOR | 477 | Test naming | Suppress in `sonar-project.properties` (Go convention) |
| `go:S1192` | CRITICAL | 180 | Duplicated literals | Extract `const` blocks in test files (180 test, 0 prod) |
| `godre:S8193` | MINOR | 104 | Unnecessary vars | Inline error declarations (7 test, 97 prod) |
| `go:S3776` | CRITICAL | 19 | Cognitive complexity | Refactor functions (extract helpers, use slices pkg) |
| `docker:S7031` | MINOR | 2 | Consecutive RUN | Merge Dockerfile RUN instructions |
| `go:S1186` | CRITICAL | 1 | Empty function | Add comment explaining why |

## Remediation Phases

### Phase 1 — Quick wins (484 issues)

**S100 (477):** Suppress via `sonar-project.properties`. Go test convention
uses `TestFoo_SubScenario` with underscores — this is idiomatic, not a real issue.

```properties
sonar.issue.ignore.multicriteria=e1
sonar.issue.ignore.multicriteria.e1.ruleKey=go:S100
sonar.issue.ignore.multicriteria.e1.resourceKey=**/*_test.go
```

**S1186 (1):** Add comment to empty function body.

**S7031 (2):** Merge consecutive Dockerfile RUN instructions.

**docker:S7031 files:**

- `e2e-images/15-workspace-vllm-tool-orchestration.Dockerfile:6`
- `runner-images/Dockerfile:43`

**go:S1186 files:**

- `internal/app/service_unit_test.go:139` — Add a nested comment explaining why this function is empty or complete the imple

### Phase 2 — Duplicated literals: `go:S1192` (180 issues)

- **Test files:** 180 issues in 42 files
- **Production files:** 0 issues in 0 files

**Strategy:** Extract duplicated string literals into file-level `const` blocks.

#### Test files

| File | Issues |
|------|--------|
| `internal/adapters/tools/image_tools_test.go` | 14 |
| `internal/adapters/tools/container_tools_test.go` | 12 |
| `internal/httpapi/server_test.go` | 11 |
| `internal/adapters/tools/ci_pipeline_tools_test.go` | 8 |
| `internal/adapters/tools/container_scan_tools_test.go` | 8 |
| `internal/httpapi/auth_test.go` | 8 |
| `internal/adapters/tools/connection_tools_resolve_test.go` | 7 |
| `internal/adapters/tools/fs_tools_test.go` | 7 |
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
| `internal/adapters/tools/repo_tools_test.go` | 4 |
| `internal/adapters/workspace/kubernetes_pod_janitor_test.go` | 4 |
| `internal/adapters/policy/static_policy_generic_test.go` | 3 |
| `internal/adapters/tools/api_benchmark_tools_test.go` | 3 |
| `internal/adapters/tools/artifact_tools_test.go` | 3 |
| `internal/adapters/tools/coverage_tools_test.go` | 3 |
| `internal/adapters/tools/repo_analysis_tools_test.go` | 3 |
| `internal/app/service_integration_test.go` | 3 |
| `internal/adapters/sessionstore/valkey_store_test.go` | 2 |
| `internal/adapters/tools/catalog_factories_test.go` | 2 |
| `internal/adapters/tools/git_tools_remote_test.go` | 2 |
| `internal/adapters/tools/git_tools_test.go` | 2 |
| `internal/adapters/tools/runner_test.go` | 2 |
| `internal/adapters/tools/swe_helpers_test.go` | 2 |
| `internal/adapters/tools/toolchain_tools_test.go` | 2 |
| `internal/adapters/policy/static_policy_extra_test.go` | 1 |
| `internal/adapters/storage/local_artifacts_test.go` | 1 |
| `internal/adapters/tools/connection_tools_test.go` | 1 |
| `internal/adapters/tools/k8s_delivery_tools_test.go` | 1 |
| `internal/adapters/tools/k8s_tools_test.go` | 1 |
| `internal/adapters/workspace/local_manager_test.go` | 1 |
| `internal/app/session_store_memory_test.go` | 1 |

#### Top duplicated literals

| Literal | Occurrences |
|---------|-------------|
| `"unexpected error: %v"` | 7 |
| `"session-1"` | 5 |
| `"unexpected error code: %s"` | 5 |
| `"tenant-a"` | 5 |
| `"go.mod"` | 4 |
| `"write go.mod failed: %v"` | 4 |
| `"unexpected error: %#v"` | 4 |
| `"module example.com/demo\n\ngo 1.23\n"` | 3 |
| `"/workspace/repo"` | 3 |
| `"expected map output, got %#v"` | 3 |
| `"main.c"` | 3 |
| `"sandbox.jobs"` | 2 |
| `"unexpected list error: %v"` | 2 |
| `".workspace-venv"` | 2 |
| `"dev.nats"` | 2 |
| `"workspace-container-run"` | 2 |
| `"expected map output, got %T"` | 2 |
| `"notes/todo.txt"` | 2 |
| `"GPL-3.0"` | 2 |
| `"tenant-runtime"` | 2 |
| `"/v1/sessions"` | 2 |
| `"workspace:test"` | 1 |
| `"fs.read"` | 1 |
| `"corr-1"` | 1 |
| `"item not allowed"` | 1 |

### Phase 3 — Unnecessary variable declarations: `godre:S8193` (104 issues)

- **Test files:** 7 issues in 5 files
- **Production files:** 97 issues in 20 files

**Strategy:** Replace `x, err := f(); if err != nil` with `if x, err := f(); err != nil`
where the variable is only used inside the if block. For test files, inline `err` checks.

#### Production files

| File | Line | Detail |
|------|------|--------|
| `internal/adapters/policy/static_policy.go` | 168 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 210 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 346 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 428 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 495 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 532 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 569 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 606 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/policy/static_policy.go` | 651 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/api_benchmark_tools.go` | 148 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/artifact_tools.go` | 417 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/connection_tools.go` | 43 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/connection_tools.go` | 80 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/connection_tools.go` | 306 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/connection_tools.go` | 326 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/container_tools.go` | 243 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/container_tools.go` | 464 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/container_tools.go` | 684 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/container_tools.go` | 767 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 147 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 341 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 452 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 559 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 667 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 794 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 954 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 1049 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 1161 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/fs_tools.go` | 1239 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 123 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 159 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 200 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 254 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 314 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 370 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 430 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 469 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 588 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 624 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/git_tools.go` | 659 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/image_tools.go` | 92 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/image_tools.go` | 394 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/image_tools.go` | 651 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/image_tools.go` | 1113 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/k8s_delivery_tools.go` | 259 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/k8s_delivery_tools.go` | 296 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/k8s_delivery_tools.go` | 333 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/k8s_delivery_tools.go` | 569 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/k8s_tools.go` | 603 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/kafka_tools.go` | 131 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/kafka_tools.go` | 300 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/kafka_tools.go` | 412 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 148 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 173 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 195 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 216 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 238 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 262 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 284 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 306 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 328 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 352 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 444 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 468 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 507 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/language_tool_handlers.go` | 549 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/mongo_tools.go` | 82 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/mongo_tools.go` | 176 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/nats_tools.go` | 80 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/nats_tools.go` | 167 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/nats_tools.go` | 263 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/rabbit_tools.go` | 109 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/rabbit_tools.go` | 220 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/rabbit_tools.go` | 325 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 104 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 202 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 296 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 408 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 492 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 564 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/redis_tools.go` | 683 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_analysis_tools.go` | 150 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_analysis_tools.go` | 231 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_analysis_tools.go` | 308 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_analysis_tools.go` | 426 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_analysis_tools.go` | 533 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_tools.go` | 68 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_tools.go` | 117 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/repo_tools.go` | 202 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/toolchain_tools.go` | 94 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/toolchain_tools.go` | 140 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/toolchain_tools.go` | 225 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/toolchain_tools.go` | 275 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/toolchain_tools.go` | 328 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/adapters/tools/toolchain_tools.go` | 404 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/httpapi/server.go` | 68 | Remove this unnecessary variable declaration and use the expression directly in the condit |
| `internal/httpapi/server.go` | 154 | Remove this unnecessary variable declaration and use the expression directly in the condit |

#### Test files

| File | Issues |
|------|--------|
| `internal/adapters/tools/image_tools_test.go` | 3 |
| `internal/adapters/tools/git_tools_test.go` | 1 |
| `internal/adapters/tools/nats_tools_test.go` | 1 |
| `internal/adapters/tools/redis_tools_test.go` | 1 |
| `internal/app/service_schema_test.go` | 1 |

### Phase 4 — Cognitive complexity: `go:S3776` (19 issues)

**Strategy:** Extract helper functions, use `slices.Contains`, reduce nesting.

| File | Line | Complexity | Message |
|------|------|-----------|---------|
| `internal/adapters/tools/catalog_factories_test.go` (test) | 15 | 56/15 | Refactor this method to reduce its Cognitive Complexity from 56 to the 15 allowe |
| `internal/adapters/tools/git_tools_test.go` (test) | 139 | 34/15 | Refactor this method to reduce its Cognitive Complexity from 34 to the 15 allowe |
| `internal/adapters/tools/api_benchmark_tools_test.go` (test) | 29 | 23/15 | Refactor this method to reduce its Cognitive Complexity from 23 to the 15 allowe |
| `internal/adapters/policy/static_policy_test.go` (test) | 76 | 20/15 | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowe |
| `internal/adapters/policy/static_policy_test.go` (test) | 242 | 20/15 | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowe |
| `internal/adapters/tools/kafka_tools_test.go` (test) | 307 | 20/15 | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowe |
| `internal/adapters/tools/license_tools_test.go` (test) | 15 | 20/15 | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowe |
| `internal/adapters/tools/container_tools_test.go` (test) | 192 | 18/15 | Refactor this method to reduce its Cognitive Complexity from 18 to the 15 allowe |
| `internal/adapters/tools/catalog_defaults_test.go` (test) | 5 | 17/15 | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowe |
| `internal/adapters/tools/image_tools_test.go` (test) | 298 | 17/15 | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowe |
| `internal/adapters/tools/image_tools_test.go` (test) | 385 | 17/15 | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowe |
| `internal/adapters/tools/kafka_tools_test.go` (test) | 204 | 17/15 | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowe |
| `internal/adapters/tools/connection_tools.go` | 232 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |
| `internal/adapters/tools/container_scan_tools_test.go` (test) | 16 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |
| `internal/adapters/tools/container_scan_tools_test.go` (test) | 74 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |
| `internal/adapters/tools/image_tools_test.go` (test) | 195 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |
| `internal/adapters/tools/kafka_tools_test.go` (test) | 370 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |
| `internal/adapters/tools/license_tools_test.go` (test) | 110 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |
| `internal/adapters/workspace/kubernetes_manager_test.go` (test) | 89 | 16/15 | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowe |

---

## Full Issue Inventory by File

### `e2e-images/15-workspace-vllm-tool-orchestration.Dockerfile` (1 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 6 | `docker:S7031` | MINOR | Merge this RUN instruction with the consecutive ones. |

### `internal/adapters/audit/logger_audit_test.go` (1 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 51 | `go:S100` | MINOR | Rename function "TestLoggerAuditRecord_RedactsSensitiveMetadata" to match the regular expr |

### `internal/adapters/invocationstore/valkey_store_test.go` (14 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 78 | `go:S100` | MINOR | Rename function "TestValkeyStore_SaveAndGet" to match the regular expression ^(_\|[a-zA-Z0- |
| 80 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "workspace:test" 7 times. |
| 84 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-1" 4 times. |
| 85 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "fs.read" 4 times. |
| 111 | `go:S100` | MINOR | Rename function "TestValkeyStore_GetMissing" to match the regular expression ^(_\|[a-zA-Z0- |
| 122 | `go:S100` | MINOR | Rename function "TestValkeyStore_SaveErrors" to match the regular expression ^(_\|[a-zA-Z0- |
| 130 | `go:S100` | MINOR | Rename function "TestValkeyStore_SaveSetNXError" to match the regular expression ^(_\|[a-zA |
| 136 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "corr-1" 3 times. |
| 143 | `go:S100` | MINOR | Rename function "TestValkeyStore_GetErrors" to match the regular expression ^(_\|[a-zA-Z0-9 |
| 151 | `go:S100` | MINOR | Rename function "TestValkeyStore_GetInvalidJSON" to match the regular expression ^(_\|[a-zA |
| 162 | `go:S100` | MINOR | Rename function "TestValkeyStore_DefaultPrefix" to match the regular expression ^(_\|[a-zA- |
| 169 | `go:S100` | MINOR | Rename function "TestNewValkeyStoreFromAddress_InvalidAddress" to match the regular expres |
| 183 | `go:S100` | MINOR | Rename function "TestValkeyStore_Key" to match the regular expression ^(_\|[a-zA-Z0-9]+)$ |
| 190 | `go:S100` | MINOR | Rename function "TestValkeyStore_FindByCorrelation" to match the regular expression ^(_\|[a |

### `internal/adapters/policy/static_policy.go` (9 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 168 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 210 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 346 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 428 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 495 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 532 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 569 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 606 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 651 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/policy/static_policy_extra_test.go` (21 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 12 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsClusterScopeForDevops" to match the regular expres |
| 24 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 26 times. |
| 31 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesHighRiskWithoutPlatformAdmin" to match the regular |
| 50 | `go:S100` | MINOR | Rename function "TestStaticPolicy_PathParsingPayloadErrors" to match the regular expressio |
| 72 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesDisallowedArgPrefix" to match the regular expressi |
| 103 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsApprovedArgPrefixes" to match the regular expressi |
| 133 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesProfileOutsideAllowlist" to match the regular expr |
| 163 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsProfileWhenAllowlistNotConfigured" to match the re |
| 190 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesSubjectOutsideAllowlist" to match the regular expr |
| 220 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsWildcardSubject" to match the regular expression ^ |
| 250 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesTopicOutsideAllowlist" to match the regular expres |
| 280 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsKeyWithinAllowedPrefix" to match the regular expre |
| 310 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesQueueOutsideAllowlist" to match the regular expres |
| 340 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesKeyOutsideAllowedPrefix" to match the regular expr |
| 370 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesNamespaceOutsideAllowlist" to match the regular ex |
| 401 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsNamespaceWithinAllowlist" to match the regular exp |
| 429 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesRegistryOutsideAllowlist" to match the regular exp |
| 460 | `go:S100` | MINOR | Rename function "TestStaticPolicy_AllowsRegistryFromImageRef" to match the regular express |
| 488 | `go:S100` | MINOR | Rename function "TestStaticPolicy_ArgValueAllowedEdgeCases" to match the regular expressio |
| 592 | `go:S100` | MINOR | Rename function "TestStaticPolicy_MultiProfileField" to match the regular expression ^(_\|[ |
| 644 | `go:S100` | MINOR | Rename function "TestStaticPolicy_ExtractStringFieldValuesErrors" to match the regular exp |

### `internal/adapters/policy/static_policy_generic_test.go` (21 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 11 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_EmptyFieldName" to match the regular expression ^( |
| 14 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 9 times. |
| 21 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_WhitespaceFieldName" to match the regular expressi |
| 31 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_FieldNotFound" to match the regular expression ^(_ |
| 41 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_SingleString" to match the regular expression ^(_\| |
| 51 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_SingleNonString" to match the regular expression ^ |
| 58 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_MultiStringArray" to match the regular expression  |
| 69 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_MultiNonArray" to match the regular expression ^(_ |
| 77 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_MultiArrayWithNonString" to match the regular expr |
| 85 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_NestedDotPath" to match the regular expression ^(_ |
| 100 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_NestedDotPathNotFound" to match the regular expres |
| 111 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_NonObjectPayload" to match the regular expression  |
| 121 | `go:S100` | MINOR | Rename function "TestExtractFieldValues_EmptyMultiArray" to match the regular expression ^ |
| 136 | `go:S100` | MINOR | Rename function "TestCheckFieldValuesAllowed_AllAllowed" to match the regular expression ^ |
| 140 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "item not allowed" 5 times. |
| 146 | `go:S100` | MINOR | Rename function "TestCheckFieldValuesAllowed_OneDenied" to match the regular expression ^( |
| 155 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected reason: %q" 3 times. |
| 159 | `go:S100` | MINOR | Rename function "TestCheckFieldValuesAllowed_ExtractionError" to match the regular express |
| 173 | `go:S100` | MINOR | Rename function "TestCheckFieldValuesAllowed_EmptyValuesSkipped" to match the regular expr |
| 183 | `go:S100` | MINOR | Rename function "TestCheckFieldValuesAllowed_FieldNotFound" to match the regular expressio |
| 193 | `go:S100` | MINOR | Rename function "TestCheckFieldValuesAllowed_SingleValue" to match the regular expression  |

### `internal/adapters/policy/static_policy_test.go` (13 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 12 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesClusterScopeWithoutRole" to match the regular expr |
| 24 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 3 times. |
| 31 | `go:S100` | MINOR | Rename function "TestStaticPolicy_RequiresApproval" to match the regular expression ^(_\|[a |
| 54 | `go:S100` | MINOR | Rename function "TestStaticPolicy_DeniesPathOutsideAllowList" to match the regular express |
| 76 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowed. |
| 76 | `go:S100` | MINOR | Rename function "TestStaticPolicy_PathAndArgExtractors" to match the regular expression ^( |
| 86 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox.jobs" 3 times. |
| 139 | `go:S100` | MINOR | Rename function "TestStaticPolicy_ArgumentPolicyRules" to match the regular expression ^(_ |
| 173 | `go:S100` | MINOR | Rename function "TestStaticPolicy_MetadataGovernancePolicies" to match the regular express |
| 177 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox.,dev." 3 times. |
| 242 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowed. |
| 242 | `go:S100` | MINOR | Rename function "TestStaticPolicy_MatchersAndUtilities" to match the regular expression ^( |
| 256 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox.todo.created" 3 times. |

### `internal/adapters/sessionstore/valkey_store_test.go` (10 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 66 | `go:S100` | MINOR | Rename function "TestValkeyStore_SaveGetDelete" to match the regular expression ^(_\|[a-zA- |
| 67 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "workspace:test:session" 3 times. |
| 70 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-1" 4 times. |
| 102 | `go:S100` | MINOR | Rename function "TestValkeyStore_GetMissing" to match the regular expression ^(_\|[a-zA-Z0- |
| 113 | `go:S100` | MINOR | Rename function "TestValkeyStore_SaveError" to match the regular expression ^(_\|[a-zA-Z0-9 |
| 121 | `go:S100` | MINOR | Rename function "TestValkeyStore_DeleteError" to match the regular expression ^(_\|[a-zA-Z0 |
| 129 | `go:S100` | MINOR | Rename function "TestValkeyStore_GetInvalidJSON" to match the regular expression ^(_\|[a-zA |
| 139 | `go:S100` | MINOR | Rename function "TestValkeyStore_DefaultPrefix" to match the regular expression ^(_\|[a-zA- |
| 146 | `go:S100` | MINOR | Rename function "TestValkeyStore_ExpiredSessionEvicted" to match the regular expression ^( |
| 169 | `go:S100` | MINOR | Rename function "TestNewValkeyStoreFromAddress_InvalidAddress" to match the regular expres |

### `internal/adapters/storage/local_artifacts_test.go` (6 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 12 | `go:S100` | MINOR | Rename function "TestLocalArtifactStore_SaveAndList" to match the regular expression ^(_\|[ |
| 34 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected list error: %v" 3 times. |
| 41 | `go:S100` | MINOR | Rename function "TestLocalArtifactStore_EmptyAndMissing" to match the regular expression ^ |
| 62 | `go:S100` | MINOR | Rename function "TestFileSHA256_Error" to match the regular expression ^(_\|[a-zA-Z0-9]+)$ |
| 69 | `go:S100` | MINOR | Rename function "TestLocalArtifactStore_ReadSuccessAndErrors" to match the regular express |
| 103 | `go:S100` | MINOR | Rename function "TestLocalArtifactStore_ListInvalidFileInfo" to match the regular expressi |

### `internal/adapters/tools/api_benchmark_tools.go` (1 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 148 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/api_benchmark_tools_test.go` (15 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 29 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 23 to the 15 allowed. |
| 29 | `go:S100` | MINOR | Rename function "TestAPIBenchmarkHandler_Success" to match the regular expression ^(_\|[a-z |
| 126 | `go:S100` | MINOR | Rename function "TestAPIBenchmarkHandler_DeniesRouteOutsideProfileScopes" to match the reg |
| 141 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error code: %s" 4 times. |
| 145 | `go:S100` | MINOR | Rename function "TestAPIBenchmarkHandler_DeniesReadOnlyUnsafeMethod" to match the regular  |
| 165 | `go:S100` | MINOR | Rename function "TestAPIBenchmarkHandler_RejectsConstraintsViolation" to match the regular |
| 182 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "constraints violation" 3 times. |
| 187 | `go:S100` | MINOR | Rename function "TestAPIBenchmarkHandler_ExecutionError" to match the regular expression ^ |
| 211 | `go:S100` | MINOR | Rename function "TestAPIBenchmarkHandler_Name" to match the regular expression ^(_\|[a-zA-Z |
| 265 | `go:S100` | MINOR | Rename function "TestNormalizeArrivalRateLoad_ValidDefaults" to match the regular expressi |
| 286 | `go:S100` | MINOR | Rename function "TestNormalizeArrivalRateLoad_RPSTooLow" to match the regular expression ^ |
| 292 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected invalid_argument, got %s"  |
| 296 | `go:S100` | MINOR | Rename function "TestNormalizeArrivalRateLoad_RPSTooHigh" to match the regular expression  |
| 309 | `go:S100` | MINOR | Rename function "TestNormalizeArrivalRateLoad_VUSTooHigh" to match the regular expression  |
| 322 | `go:S100` | MINOR | Rename function "TestNormalizeArrivalRateLoad_ExplicitVUs" to match the regular expression |

### `internal/adapters/tools/artifact_tools.go` (1 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 417 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/artifact_tools_test.go` (15 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 18 | `go:S100` | MINOR | Rename function "TestArtifactUploadHandler_UploadsFileAsArtifact" to match the regular exp |
| 22 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "mkdir: %v" 3 times. |
| 26 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write file: %v" 5 times. |
| 64 | `go:S100` | MINOR | Rename function "TestArtifactUploadHandler_PathRequired" to match the regular expression ^ |
| 80 | `go:S100` | MINOR | Rename function "TestArtifactDownloadHandler_Base64" to match the regular expression ^(_\|[ |
| 113 | `go:S100` | MINOR | Rename function "TestArtifactDownloadHandler_UTF8Truncated" to match the regular expressio |
| 142 | `go:S100` | MINOR | Rename function "TestArtifactListHandler_ListsPattern" to match the regular expression ^(_ |
| 145 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "dist/a.txt" 3 times. |
| 187 | `go:S100` | MINOR | Rename function "TestArtifactDownloadHandler_KubernetesRequiresRunner" to match the regula |
| 206 | `go:S100` | MINOR | Rename function "TestArtifactHandlers_Names" to match the regular expression ^(_\|[a-zA-Z0- |
| 218 | `go:S100` | MINOR | Rename function "TestArtifactListHandler_KubernetesRemoteListing" to match the regular exp |
| 249 | `go:S100` | MINOR | Rename function "TestArtifactHelpers_MinInt" to match the regular expression ^(_\|[a-zA-Z0- |
| 269 | `go:S100` | MINOR | Rename function "TestCollectFlatArtifactEntries_TwoFiles" to match the regular expression  |
| 299 | `go:S100` | MINOR | Rename function "TestCollectFlatArtifactEntries_MaxEntriesLimit" to match the regular expr |
| 318 | `go:S100` | MINOR | Rename function "TestCollectFlatArtifactEntries_NonExistentDirectory" to match the regular |

### `internal/adapters/tools/build_tools_test.go` (17 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 16 | `go:S100` | MINOR | Rename function "TestRepoStaticAnalysisHandler_Go" to match the regular expression ^(_\|[a- |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "go.mod" 3 times. |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "module example.com/demo\n\ngo 1.23\ |
| 19 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write go.mod failed: %v" 3 times. |
| 41 | `go:S100` | MINOR | Rename function "TestRepoPackageHandler_C" to match the regular expression ^(_\|[a-zA-Z0-9] |
| 43 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "int main(void){return 0;}" 3 times. |
| 71 | `go:S100` | MINOR | Rename function "TestRepoStaticAnalysisHandler_RunErrorMapping" to match the regular expre |
| 87 | `go:S100` | MINOR | Rename function "TestRepoPackageHandler_NodeArtifactDetection" to match the regular expres |
| 111 | `go:S100` | MINOR | Rename function "TestRepoPackageHandler_MkdirFailure" to match the regular expression ^(_\| |
| 130 | `go:S100` | MINOR | Rename function "TestStaticAnalysisCommandForProject_AllToolchains" to match the regular e |
| 136 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal ".workspace-venv" 4 times. |
| 179 | `go:S100` | MINOR | Rename function "TestPackageCommandForProject_AllToolchains" to match the regular expressi |
| 251 | `go:S100` | MINOR | Rename function "TestRepoStaticAnalysisHandler_InvalidArgs" to match the regular expressio |
| 259 | `go:S100` | MINOR | Rename function "TestRepoStaticAnalysisHandler_NoToolchain" to match the regular expressio |
| 267 | `go:S100` | MINOR | Rename function "TestRepoPackageHandler_InvalidArgs" to match the regular expression ^(_\|[ |
| 275 | `go:S100` | MINOR | Rename function "TestRepoPackageHandler_NoToolchain" to match the regular expression ^(_\|[ |
| 283 | `go:S100` | MINOR | Rename function "TestDetectNodePackageArtifact_NoMatch" to match the regular expression ^( |

### `internal/adapters/tools/catalog_defaults_test.go` (2 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 5 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowed. |
| 5 | `go:S100` | MINOR | Rename function "TestDefaultCapabilities_Metadata" to match the regular expression ^(_\|[a- |

### `internal/adapters/tools/catalog_factories_test.go` (6 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 15 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 56 to the 15 allowed. |
| 15 | `go:S100` | MINOR | Rename function "TestDefaultCapabilities_PolicyConsistency" to match the regular expressio |
| 26 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "missing capability %q" 6 times. |
| 93 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "%s: expected lang tool OutputSchema |
| 128 | `go:S100` | MINOR | Rename function "TestDefaultCapabilities_ShellDenyChars" to match the regular expression ^ |
| 156 | `go:S100` | MINOR | Rename function "TestDefaultCapabilities_LangToolOutputSchema" to match the regular expres |

### `internal/adapters/tools/ci_pipeline_tools_test.go` (27 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 16 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_FailFast" to match the regular expression ^(_\|[a |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "module example.com/demo\n\ngo 1.23\ |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "go.mod" 9 times. |
| 19 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write go.mod failed: %v" 5 times. |
| 26 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "validate ok" 7 times. |
| 58 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_QualityGateFailure" to match the regular express |
| 70 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "build ok" 5 times. |
| 72 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tests ok" 4 times. |
| 115 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_NoSupportedToolchain" to match the regular expre |
| 123 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_SuccessPath" to match the regular expression ^(_ |
| 172 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_InvalidArgs" to match the regular expression ^(_ |
| 180 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_StaticAnalysisFailFast" to match the regular exp |
| 197 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "exit 1" 6 times. |
| 218 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_CoverageStep" to match the regular expression ^( |
| 255 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_NonGoSkipsCoverage" to match the regular express |
| 283 | `go:S100` | MINOR | Rename function "TestUpdatePipelineQualityMetrics_AllSteps" to match the regular expressio |
| 321 | `go:S100` | MINOR | Rename function "TestAnnotatePipelineStepError_AllBranches" to match the regular expressio |
| 342 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_StaticAnalysisSkippedNoToolchain" to match the r |
| 379 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_FailFastFalse_ContinuesAfterFailure" to match th |
| 384 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write go.mod: %v" 4 times. |
| 436 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_BuildCommandResolutionError" to match the regula |
| 464 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_TestStepFailFast" to match the regular expressio |
| 503 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_ValidateFailNoAbort" to match the regular expres |
| 536 | `go:S100` | MINOR | Rename function "TestCIRunPipelineHandler_ValidateFailFast" to match the regular expressio |
| 580 | `go:S100` | MINOR | Rename function "TestRunPipelineStaticStep_SkippedOnUnsupported" to match the regular expr |
| 595 | `go:S100` | MINOR | Rename function "TestRunPipelineBuildStep_CommandResolutionError" to match the regular exp |
| 607 | `go:S100` | MINOR | Rename function "TestRunPipelineTestStep_CommandResolutionError" to match the regular expr |

### `internal/adapters/tools/connection_tools.go` (5 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 43 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 80 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 232 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 306 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 326 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/connection_tools_resolve_test.go` (24 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 15 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_EmptyID" to match the regular expression ^(_\|[a-z |
| 17 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "dev.nats" 19 times. |
| 23 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_WhitespaceID" to match the regular expression ^(_ |
| 31 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_NotFound" to match the regular expression ^(_\|[a- |
| 39 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_KindMismatch" to match the regular expression ^(_ |
| 51 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_EndpointNotConfigured" to match the regular expre |
| 56 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "nats://fallback:4222" 5 times. |
| 62 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_DefaultEndpointFallback" to match the regular exp |
| 67 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %#v" 3 times. |
| 77 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_EnvEndpointUsed" to match the regular expression  |
| 95 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_MultipleAllowedKinds" to match the regular expres |
| 111 | `go:S100` | MINOR | Rename function "TestResolveTypedProfile_FilteredByAllowlist" to match the regular express |
| 171 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "example.com" 7 times. |
| 197 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "*.example.com" 3 times. |
| 200 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "10.0.0.0/24" 3 times. |
| 218 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_NoEnvVar" to match the regular expression ^(_\| |
| 226 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_InvalidJSON" to match the regular expression ^ |
| 235 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_MissingProfile" to match the regular expressio |
| 244 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_AllowlistDenied" to match the regular expressi |
| 257 | `go:S100` | MINOR | Rename function "TestProfileEndpointAllowed_NoAllowlist" to match the regular expression ^ |
| 260 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "nats://any.host:4222" 3 times. |
| 265 | `go:S100` | MINOR | Rename function "TestProfileEndpointAllowed_InvalidAllowlistJSON" to match the regular exp |
| 273 | `go:S100` | MINOR | Rename function "TestProfileEndpointAllowed_ProfileNotInAllowlist" to match the regular ex |
| 281 | `go:S100` | MINOR | Rename function "TestProfileEndpointAllowed_EmptyEndpoint" to match the regular expression |

### `internal/adapters/tools/connection_tools_test.go` (5 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 12 | `go:S100` | MINOR | Rename function "TestConnListProfiles_DefaultProfiles" to match the regular expression ^(_ |
| 31 | `go:S100` | MINOR | Rename function "TestConnListProfiles_FilteredByAllowlist" to match the regular expression |
| 36 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "dev.redis" 3 times. |
| 51 | `go:S100` | MINOR | Rename function "TestConnDescribeProfile_ValidationAndNotFound" to match the regular expre |
| 66 | `go:S100` | MINOR | Rename function "TestConnDescribeProfile_Success" to match the regular expression ^(_\|[a-z |

### `internal/adapters/tools/container_scan_tools_test.go` (35 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 16 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 16 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_HeuristicFallbackWhenTrivyMissing" to ma |
| 20 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write Dockerfile failed: %v" 3 time |
| 38 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "./Dockerfile\n" 5 times. |
| 66 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "heuristic-dockerfile" 4 times. |
| 74 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 74 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_HeuristicFallbackWhenTrivyHasNoFindings" |
| 133 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_InvalidSeverity" to match the regular ex |
| 141 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_TrivyPath" to match the regular expressi |
| 164 | `go:S100` | MINOR | Rename function "TestParseTrivyFindings_AppliesSeverityThreshold" to match the regular exp |
| 182 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "did not expect truncation" 5 times. |
| 192 | `go:S100` | MINOR | Rename function "TestParseTrivyFindings_WithMisconfigAndSecrets" to match the regular expr |
| 230 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_InvalidArgs" to match the regular expres |
| 238 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_InvalidPath" to match the regular expres |
| 246 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_TrivyWithImageRef" to match the regular  |
| 264 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %#v" 3 times. |
| 275 | `go:S100` | MINOR | Rename function "TestSecurityScanContainerHandler_TrivyParseFailFallsBackToHeuristic" to m |
| 308 | `go:S100` | MINOR | Rename function "TestParseTrivyFindings_EmptyOutput" to match the regular expression ^(_\|[ |
| 315 | `go:S100` | MINOR | Rename function "TestParseTrivyFindings_InvalidJSON" to match the regular expression ^(_\|[ |
| 322 | `go:S100` | MINOR | Rename function "TestParseTrivyFindings_Truncation" to match the regular expression ^(_\|[a |
| 330 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 4 times. |
| 336 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected 2 findings, got %d" 3 time |
| 344 | `go:S100` | MINOR | Rename function "TestDockerfileHeuristicRule_AllBranches" to match the regular expression  |
| 394 | `go:S100` | MINOR | Rename function "TestIsDockerfileCandidate_AllBranches" to match the regular expression ^( |
| 414 | `go:S100` | MINOR | Rename function "TestApplyHeuristicFallback_ScanHeuristicsError" to match the regular expr |
| 435 | `go:S100` | MINOR | Rename function "TestApplyHeuristicFallback_EmptyExistingCommand" to match the regular exp |
| 472 | `go:S100` | MINOR | Rename function "TestScanContainerHeuristics_FindCommandError" to match the regular expres |
| 487 | `go:S100` | MINOR | Rename function "TestScanContainerHeuristics_NoDockerfilesFound" to match the regular expr |
| 512 | `go:S100` | MINOR | Rename function "TestScanContainerHeuristics_CatCommandError" to match the regular express |
| 542 | `go:S100` | MINOR | Rename function "TestScanContainerHeuristics_Truncation" to match the regular expression ^ |
| 576 | `go:S100` | MINOR | Rename function "TestScanDockerfileContent_MissingUser" to match the regular expression ^( |
| 585 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "dockerfile.missing_user" 3 times. |
| 594 | `go:S100` | MINOR | Rename function "TestScanDockerfileContent_CommentAndBlankLinesSkipped" to match the regul |
| 609 | `go:S100` | MINOR | Rename function "TestScanDockerfileContent_TruncationFromMissingUser" to match the regular |
| 628 | `go:S100` | MINOR | Rename function "TestScanDockerfileContent_MissingUserTruncation" to match the regular exp |

### `internal/adapters/tools/container_tools.go` (4 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 243 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 464 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 684 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 767 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/container_tools_test.go` (36 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 34 | `go:S100` | MINOR | Rename function "TestContainerPSHandler_SimulatedWhenRuntimeUnavailable" to match the regu |
| 38 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "cannot connect to runtime" 4 times. |
| 38 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "exit status 1" 5 times. |
| 40 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected command" 8 times. |
| 58 | `go:S100` | MINOR | Rename function "TestContainerPSHandler_RuntimeAndTruncation" to match the regular express |
| 78 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected podman runtime output, got |
| 89 | `go:S100` | MINOR | Rename function "TestContainerRunHandler_StrictNoRuntimeFails" to match the regular expres |
| 105 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected execution_failed, got %s"  |
| 109 | `go:S100` | MINOR | Rename function "TestContainerRunHandler_UsesRuntime" to match the regular expression ^(_\| |
| 139 | `go:S100` | MINOR | Rename function "TestContainerLogsHandler_SimulatedID" to match the regular expression ^(_ |
| 154 | `go:S100` | MINOR | Rename function "TestContainerExecHandler_DeniesDisallowedCommand" to match the regular ex |
| 165 | `go:S100` | MINOR | Rename function "TestContainerExecHandler_UsesRuntime" to match the regular expression ^(_ |
| 192 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 18 to the 15 allowed. |
| 192 | `go:S100` | MINOR | Rename function "TestContainerRunHandler_UsesKubernetesPodRuntime" to match the regular ex |
| 234 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected k8s runtime output, got %# |
| 251 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "busybox:1.36" 5 times. |
| 259 | `go:S100` | MINOR | Rename function "TestContainerPSHandler_UsesKubernetesPodRuntime" to match the regular exp |
| 266 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "workspace-container-run" 3 times. |
| 267 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-k8s-ps" 3 times. |
| 340 | `go:S100` | MINOR | Rename function "TestContainerExecHandler_UsesKubernetesPodRuntime" to match the regular e |
| 395 | `go:S100` | MINOR | Rename function "TestContainerPSHandler_StrictByDefaultEnvFailsWithoutRuntime" to match th |
| 416 | `go:S100` | MINOR | Rename function "TestContainerPSHandler_SyntheticFallbackDisabledEnvForcesStrict" to match |
| 437 | `go:S100` | MINOR | Rename function "TestContainerLogsHandler_SyntheticFallbackDisabledEnvForcesStrict" to mat |
| 458 | `go:S100` | MINOR | Rename function "TestContainerExecHandler_DeniesShellCommands" to match the regular expres |
| 485 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "busybox:latest" 3 times. |
| 687 | `go:S100` | MINOR | Rename function "TestWaitForK8sContainerPodTerminal_AlreadyTerminated" to match the regula |
| 695 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 3 times. |
| 702 | `go:S100` | MINOR | Rename function "TestWaitForK8sContainerPodTerminal_AlreadyFailed" to match the regular ex |
| 732 | `go:S100` | MINOR | Rename function "TestBuildSimulatedContainerRunResult_Detach" to match the regular express |
| 735 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "nginx:latest" 4 times. |
| 756 | `go:S100` | MINOR | Rename function "TestBuildSimulatedContainerRunResult_NonDetach" to match the regular expr |
| 772 | `go:S100` | MINOR | Rename function "TestHandleContainerRunError_NonStrict" to match the regular expression ^( |
| 791 | `go:S100` | MINOR | Rename function "TestHandleContainerRunError_Strict" to match the regular expression ^(_\|[ |
| 813 | `go:S100` | MINOR | Rename function "TestInvokeK8sLogs_PodNotFound" to match the regular expression ^(_\|[a-zA- |
| 830 | `go:S100` | MINOR | Rename function "TestInvokeK8sLogs_PodExists_ReturnsOutput" to match the regular expressio |
| 861 | `go:S100` | MINOR | Rename function "TestInvokeK8sLogs_GenericFetchError" to match the regular expression ^(_\| |

### `internal/adapters/tools/coverage_tools_test.go` (9 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 15 | `go:S100` | MINOR | Rename function "TestRepoCoverageReportHandler_Go" to match the regular expression ^(_\|[a- |
| 17 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "go.mod" 3 times. |
| 17 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "module example.com/demo\n\ngo 1.23\ |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write go.mod failed: %v" 3 times. |
| 61 | `go:S100` | MINOR | Rename function "TestRepoCoverageReportHandler_GoCoverCommandFailure" to match the regular |
| 95 | `go:S100` | MINOR | Rename function "TestRepoCoverageReportHandler_NonGoPath" to match the regular expression  |
| 122 | `go:S100` | MINOR | Rename function "TestRepoCoverageReportHandler_InvalidArgs" to match the regular expressio |
| 130 | `go:S100` | MINOR | Rename function "TestRepoCoverageReportHandler_NoToolchain" to match the regular expressio |
| 138 | `go:S100` | MINOR | Rename function "TestRepoCoverageReportHandler_GoTestFailure" to match the regular express |

### `internal/adapters/tools/dependency_tools_test.go` (38 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 17 | `go:S100` | MINOR | Rename function "TestSecurityScanDependenciesHandler_Go" to match the regular expression ^ |
| 49 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected map output, got %T" 3 time |
| 59 | `go:S100` | MINOR | Rename function "TestSBOMGenerateHandler_GeneratesCycloneDXArtifact" to match the regular  |
| 108 | `go:S100` | MINOR | Rename function "TestSBOMGenerateHandler_RejectsUnsupportedFormat" to match the regular ex |
| 121 | `go:S100` | MINOR | Rename function "TestSecurityScanDependenciesHandler_InvalidPath" to match the regular exp |
| 129 | `go:S100` | MINOR | Rename function "TestSBOMGenerateHandler_RejectsInvalidPath" to match the regular expressi |
| 137 | `go:S100` | MINOR | Rename function "TestSBOMGenerateHandler_InventoryParseFailure" to match the regular expre |
| 153 | `go:S100` | MINOR | Rename function "TestSecurityScanDependenciesHandler_ParseFailure" to match the regular ex |
| 215 | `go:S100` | MINOR | Rename function "TestSecurityScanDependenciesHandler_InvalidArgs" to match the regular exp |
| 223 | `go:S100` | MINOR | Rename function "TestSecurityScanDependenciesHandler_NoToolchain" to match the regular exp |
| 231 | `go:S100` | MINOR | Rename function "TestSBOMGenerateHandler_InvalidArgs" to match the regular expression ^(_\| |
| 239 | `go:S100` | MINOR | Rename function "TestSBOMGenerateHandler_NoToolchain" to match the regular expression ^(_\| |
| 248 | `go:S100` | MINOR | Rename function "TestDependencyPURL_AllEcosystems" to match the regular expression ^(_\|[a- |
| 270 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_UnsupportedToolchain" to match the regular |
| 278 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_WithSubpath" to match the regular expressi |
| 286 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 11 times. |
| 293 | `go:S100` | MINOR | Rename function "TestParseGradleCoordinate_VersionOverride" to match the regular expressio |
| 358 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_PythonBranch" to match the regular express |
| 376 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected 2 dependencies, got %d" 3  |
| 383 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_RustBranch" to match the regular expressio |
| 408 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_JavaMavenBranch" to match the regular expr |
| 417 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "[INFO] org.apache.commons:commons-l |
| 433 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_JavaGradleBranch" to match the regular exp |
| 442 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "+--- org.slf4j:slf4j-api:1.7.36\n"  |
| 458 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_ParseErrorNoRunError" to match the regular |
| 472 | `go:S100` | MINOR | Rename function "TestCollectDependencyInventory_RunErrorWithEmptyDeps" to match the regula |
| 496 | `go:S100` | MINOR | Rename function "TestBuildSBOMResult_PreviewTruncation" to match the regular expression ^( |
| 538 | `go:S100` | MINOR | Rename function "TestWalkNodeDependencies_Truncation" to match the regular expression ^(_\| |
| 562 | `go:S100` | MINOR | Rename function "TestExtractNodePackageNode_NonMapValue" to match the regular expression ^ |
| 588 | `go:S100` | MINOR | Rename function "TestAppendNodeDependencyEntry_Duplicate" to match the regular expression  |
| 605 | `go:S100` | MINOR | Rename function "TestAppendNodeDependencyEntry_EmptyVersion" to match the regular expressi |
| 623 | `go:S100` | MINOR | Rename function "TestParseRustDependencyInventory_DuplicatesAndTruncation" to match the re |
| 634 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected truncated=true" 3 times. |
| 638 | `go:S100` | MINOR | Rename function "TestParseRustDependencyInventory_VPrefixStripping" to match the regular e |
| 658 | `go:S100` | MINOR | Rename function "TestParseMavenDependencyInventory_SpacesInGroupSkipped" to match the regu |
| 673 | `go:S100` | MINOR | Rename function "TestParseMavenDependencyInventory_LessThan4PartsSkipped" to match the reg |
| 684 | `go:S100` | MINOR | Rename function "TestParseMavenDependencyInventory_DuplicatesAndTruncation" to match the r |
| 706 | `go:S100` | MINOR | Rename function "TestParseGradleDependencyInventory_DuplicatesAndTruncation" to match the  |

### `internal/adapters/tools/fs_tools.go` (10 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 147 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 341 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 452 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 559 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 667 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 794 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 954 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 1049 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 1161 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 1239 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/fs_tools_test.go` (15 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 28 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "notes/todo.txt" 5 times. |
| 29 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "hola\nTODO: test" 4 times. |
| 123 | `go:S100` | MINOR | Rename function "TestFSHandlers_KubernetesRuntimeUsesCommandRunner" to match the regular e |
| 125 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/workspace/repo" 3 times. |
| 197 | `go:S100` | MINOR | Rename function "TestFSHandlers_KubernetesRuntimeRequiresRunner" to match the regular expr |
| 209 | `go:S100` | MINOR | Rename function "TestFSPatchHandler_ValidationAndExecution" to match the regular expressio |
| 228 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "@@ -1 +1 @@" 3 times. |
| 229 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "-hello" 3 times. |
| 238 | `go:S100` | MINOR | Rename function "TestFSPatchHandler_UsesRunnerAndStrategy" to match the regular expression |
| 275 | `go:S100` | MINOR | Rename function "TestFSPatchHandler_MapsRunnerErrors" to match the regular expression ^(_\| |
| 311 | `go:S100` | MINOR | Rename function "TestFSOps_LocalLifecycle" to match the regular expression ^(_\|[a-zA-Z0-9] |
| 352 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tmp/archive/input.copy.txt" 4 times |
| 391 | `go:S100` | MINOR | Rename function "TestFSOps_ValidationAndPolicy" to match the regular expression ^(_\|[a-zA- |
| 447 | `go:S100` | MINOR | Rename function "TestFSOps_KubernetesRuntimeUsesCommandRunner" to match the regular expres |
| 499 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "notes/todo.moved.txt" 3 times. |

### `internal/adapters/tools/git_tools.go` (11 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 123 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 159 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 200 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 254 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 314 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 370 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 430 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 469 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 588 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 624 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 659 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/git_tools_remote_test.go` (15 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 18 | `go:S100` | MINOR | Rename function "TestExecuteGitRemoteCommand_DefaultRemote" to match the regular expressio |
| 44 | `go:S100` | MINOR | Rename function "TestExecuteGitRemoteCommand_RemoteDenied" to match the regular expression |
| 66 | `go:S100` | MINOR | Rename function "TestExecuteGitRemoteCommand_RefspecDenied" to match the regular expressio |
| 92 | `go:S100` | MINOR | Rename function "TestExecuteGitRemoteCommand_PushWithFlags" to match the regular expressio |
| 105 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "HEAD:refs/heads/main" 5 times. |
| 119 | `go:S100` | MINOR | Rename function "TestExecuteGitRemoteCommand_PullAfterPush" to match the regular expressio |
| 153 | `go:S100` | MINOR | Rename function "TestExecuteGitRemoteCommand_CommandFails" to match the regular expression |
| 179 | `go:S100` | MINOR | Rename function "TestGitPushHandler_EmptyRemoteValidation" to match the regular expression |
| 198 | `go:S100` | MINOR | Rename function "TestGitFetchHandler_WithPrune" to match the regular expression ^(_\|[a-zA- |
| 219 | `go:S100` | MINOR | Rename function "TestGitPullHandler_WithRebase" to match the regular expression ^(_\|[a-zA- |
| 250 | `go:S100` | MINOR | Rename function "TestGitPushHandler_ForceWithLease" to match the regular expression ^(_\|[a |
| 272 | `go:S100` | MINOR | Rename function "TestGitPushHandler_InvalidJSON" to match the regular expression ^(_\|[a-zA |
| 279 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected invalid_argument for bad J |
| 283 | `go:S100` | MINOR | Rename function "TestGitFetchHandler_InvalidJSON" to match the regular expression ^(_\|[a-z |
| 294 | `go:S100` | MINOR | Rename function "TestGitPullHandler_InvalidJSON" to match the regular expression ^(_\|[a-zA |

### `internal/adapters/tools/git_tools_test.go` (8 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 18 | `go:S100` | MINOR | Rename function "TestGitHandlers_StatusDiffApplyPatch" to match the regular expression ^(_ |
| 23 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "main.txt" 6 times. |
| 54 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 67 | `go:S100` | MINOR | Rename function "TestGitHandlers_ValidationAndFailures" to match the regular expression ^( |
| 139 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 34 to the 15 allowed. |
| 139 | `go:S100` | MINOR | Rename function "TestGitHandlers_LifecycleOperations" to match the regular expression ^(_\| |
| 149 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "feature/lifecycle" 3 times. |
| 271 | `go:S100` | MINOR | Rename function "TestGitHandlers_AllowlistPolicies" to match the regular expression ^(_\|[a |

### `internal/adapters/tools/image_tools.go` (4 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 92 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 394 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 651 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 1113 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/image_tools_test.go` (33 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 27 | `go:S100` | MINOR | Rename function "TestImageInspectHandler_Dockerfile" to match the regular expression ^(_\|[ |
| 44 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/workspace/repo" 11 times. |
| 56 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected map output, got %T" 11 tim |
| 69 | `go:S100` | MINOR | Rename function "TestImageInspectHandler_ImageRef" to match the regular expression ^(_\|[a- |
| 97 | `go:S100` | MINOR | Rename function "TestImageBuildHandler_UsesBuilderWhenAvailable" to match the regular expr |
| 109 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "buildah version 1.36.0" 4 times. |
| 116 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected command: %s %#v" 9 times |
| 144 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected exit_code=0, got %#v" 6 ti |
| 148 | `go:S100` | MINOR | Rename function "TestImageBuildHandler_SyntheticFallbackWithoutBuilder" to match the regul |
| 155 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "FROM alpine:3.20\nRUN echo fallback |
| 158 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "not found" 9 times. |
| 178 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected synthetic builder, got %#v |
| 181 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected simulated=true, got %#v" 6 |
| 184 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected push_skipped_reason: %#v |
| 195 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 195 | `go:S100` | MINOR | Rename function "TestImageBuildHandler_FallbacksToSyntheticWhenPodmanUserNamespaceUnsuppor |
| 208 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected buildah args: %#v" 3 tim |
| 218 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "command failed: Error during unshar |
| 251 | `go:S100` | MINOR | Rename function "TestImageBuildHandler_FallbacksToSyntheticWhenBuildahUserNamespaceUnsuppo |
| 298 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowed. |
| 298 | `go:S100` | MINOR | Rename function "TestImagePushHandler_UsesBuilderWhenAvailable" to match the regular expre |
| 348 | `go:S100` | MINOR | Rename function "TestImagePushHandler_SyntheticFallbackWithoutBuilder" to match the regula |
| 378 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected pushed=false, got %#v" 3 t |
| 385 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowed. |
| 385 | `go:S100` | MINOR | Rename function "TestImagePushHandler_FallbacksToSyntheticWhenPodmanUserNamespaceUnsupport |
| 437 | `go:S100` | MINOR | Rename function "TestImagePushHandler_FallbacksToSyntheticWhenBuildahUserNamespaceUnsuppor |
| 483 | `go:S100` | MINOR | Rename function "TestImagePushHandler_StrictFailsWithoutBuilder" to match the regular expr |
| 514 | `go:S100` | MINOR | Rename function "TestImageHandlers_NamesAndCommandBuilders" to match the regular expressio |
| 525 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "ghcr.io/acme/demo:1.0.0" 7 times. |
| 549 | `go:S100` | MINOR | Rename function "TestImageHelper_DefaultTagAndValidation" to match the regular expression  |
| 560 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 563 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 566 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/k8s_delivery_tools.go` (4 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 259 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 296 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 333 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 569 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/k8s_delivery_tools_test.go` (11 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 17 | `go:S100` | MINOR | Rename function "TestK8sApplyManifestHandler_ConfigMapCreateAndUpdate" to match the regula |
| 79 | `go:S100` | MINOR | Rename function "TestK8sApplyManifestHandler_DeniesUnsupportedKind" to match the regular e |
| 105 | `go:S100` | MINOR | Rename function "TestK8sApplyManifestHandler_DeniesNamespaceMismatch" to match the regular |
| 131 | `go:S100` | MINOR | Rename function "TestK8sRolloutStatusHandler_Succeeds" to match the regular expression ^(_ |
| 170 | `go:S100` | MINOR | Rename function "TestK8sRolloutStatusHandler_Timeout" to match the regular expression ^(_\| |
| 205 | `go:S100` | MINOR | Rename function "TestK8sRestartDeploymentHandler_Succeeds" to match the regular expression |
| 269 | `go:S100` | MINOR | Rename function "TestK8sApplyManifestHandler_DeploymentCreateAndUpdate" to match the regul |
| 320 | `go:S100` | MINOR | Rename function "TestK8sApplyManifestHandler_ServiceCreateAndUpdate" to match the regular  |
| 366 | `go:S100` | MINOR | Rename function "TestPreserveServiceImmutableFields_CopiesFields" to match the regular exp |
| 370 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "10.0.0.1" 4 times. |
| 414 | `go:S100` | MINOR | Rename function "TestK8sApplyManifestHandler_EmptyManifestAndNoObjects" to match the regul |

### `internal/adapters/tools/k8s_tools.go` (1 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 603 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/k8s_tools_test.go` (8 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 17 | `go:S100` | MINOR | Rename function "TestK8sGetPodsHandler_ListPods" to match the regular expression ^(_\|[a-zA |
| 109 | `go:S100` | MINOR | Rename function "TestK8sGetPodsHandler_Truncates" to match the regular expression ^(_\|[a-z |
| 137 | `go:S100` | MINOR | Rename function "TestK8sGetPodsHandler_WithoutClientFails" to match the regular expression |
| 151 | `go:S100` | MINOR | Rename function "TestK8sGetServicesHandler_ListAndTruncate" to match the regular expressio |
| 187 | `go:S100` | MINOR | Rename function "TestK8sGetDeploymentsHandler_ListDeployments" to match the regular expres |
| 198 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "ghcr.io/acme/api:1" 5 times. |
| 232 | `go:S100` | MINOR | Rename function "TestK8sGetImagesHandler_Aggregates" to match the regular expression ^(_\|[ |
| 272 | `go:S100` | MINOR | Rename function "TestK8sGetLogsHandler_RequiresPodName" to match the regular expression ^( |

### `internal/adapters/tools/kafka_tools.go` (3 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 131 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 300 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 412 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/kafka_tools_test.go` (19 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 42 | `go:S100` | MINOR | Rename function "TestKafkaConsumeHandler_Success" to match the regular expression ^(_\|[a-z |
| 45 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox.events" 7 times. |
| 64 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected map output, got %#v" 3 tim |
| 77 | `go:S100` | MINOR | Rename function "TestKafkaConsumeHandler_DeniesTopicOutsideProfileScopes" to match the reg |
| 88 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error code: %s" 4 times. |
| 92 | `go:S100` | MINOR | Rename function "TestKafkaProduceHandler_Success" to match the regular expression ^(_\|[a-z |
| 122 | `go:S100` | MINOR | Rename function "TestKafkaProduceHandler_DeniesReadOnlyProfile" to match the regular expre |
| 140 | `go:S100` | MINOR | Rename function "TestKafkaProduceHandler_ExecutionError" to match the regular expression ^ |
| 156 | `go:S100` | MINOR | Rename function "TestKafkaTopicMetadataHandler_Success" to match the regular expression ^( |
| 185 | `go:S100` | MINOR | Rename function "TestKafkaConsumeHandler_MapsExecutionErrors" to match the regular express |
| 204 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowed. |
| 204 | `go:S100` | MINOR | Rename function "TestKafkaConsumeHandler_OffsetModes" to match the regular expression ^(_\| |
| 258 | `go:S100` | MINOR | Rename function "TestKafkaHandlers_NamesAndLiveClientErrors" to match the regular expressi |
| 272 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "127.0.0.1:1" 3 times. |
| 307 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowed. |
| 307 | `go:S100` | MINOR | Rename function "TestKafkaHelpers_ProfileResolutionAndPatterning" to match the regular exp |
| 370 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 370 | `go:S100` | MINOR | Rename function "TestParseKafkaOffsetInput_AllBranches" to match the regular expression ^( |
| 430 | `go:S100` | MINOR | Rename function "TestKafkaTopicMetadataHandler_ErrorPaths" to match the regular expression |

### `internal/adapters/tools/language_tool_handlers.go` (14 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 148 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 173 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 195 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 216 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 238 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 262 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 284 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 306 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 328 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 352 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 444 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 468 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 507 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 549 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/language_tool_handlers_test.go` (15 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 28 | `go:S100` | MINOR | Rename function "TestRustBuildHandler_BuildsExpectedCommand" to match the regular expressi |
| 41 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected exit code 0, got %d" 5 tim |
| 44 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected one runner call, got %d" 4 |
| 57 | `go:S100` | MINOR | Rename function "TestNodeInstallHandler_UsesInstallWhenUseCIFalse" to match the regular ex |
| 86 | `go:S100` | MINOR | Rename function "TestNodeTypecheckHandler_AppendsTargetAfterDoubleDash" to match the regul |
| 114 | `go:S100` | MINOR | Rename function "TestCBuildHandler_CompilesRequestedSource" to match the regular expressio |
| 116 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "main.c" 3 times. |
| 150 | `go:S100` | MINOR | Rename function "TestCTestHandler_CompilesAndExecutesBinary" to match the regular expressi |
| 152 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "todo_test.c" 3 times. |
| 229 | `go:S100` | MINOR | Rename function "TestRustHandlers_Commands" to match the regular expression ^(_\|[a-zA-Z0-9 |
| 260 | `go:S100` | MINOR | Rename function "TestNodeHandlers_Commands" to match the regular expression ^(_\|[a-zA-Z0-9 |
| 264 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "apps/web" 3 times. |
| 323 | `go:S100` | MINOR | Rename function "TestPythonInstallDepsHandler_ValidationAndInstallPaths" to match the regu |
| 366 | `go:S100` | MINOR | Rename function "TestPythonTestHandler_InvalidPattern" to match the regular expression ^(_ |
| 377 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal ".workspace-venv" 3 times. |

### `internal/adapters/tools/license_tools_test.go` (30 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 15 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 20 to the 15 allowed. |
| 15 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_DeniedLicenseFailsStatus" to match the re |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "package.json" 3 times. |
| 55 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "GPL-3.0" 3 times. |
| 77 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_InvalidUnknownPolicy" to match the regula |
| 85 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_UnknownPolicyDeny" to match the regular e |
| 110 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 136 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "left-pad" 4 times. |
| 188 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_InvalidArgs" to match the regular express |
| 196 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_InvalidPath" to match the regular express |
| 204 | `go:S100` | MINOR | Rename function "TestCheckTokensAgainstAllowed_AllBranches" to match the regular expressio |
| 217 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected unknown, got %q" 3 times. |
| 234 | `go:S100` | MINOR | Rename function "TestParseLicenseCheckRequest_AllBranches" to match the regular expression |
| 238 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %#v" 5 times. |
| 301 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_EnrichmentFailsButEntriesExist" to match  |
| 352 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_InventoryRunErrWithEmptyEnriched" to matc |
| 399 | `go:S100` | MINOR | Rename function "TestSecurityLicenseCheckHandler_WarnStatus" to match the regular expressi |
| 450 | `go:S100` | MINOR | Rename function "TestEnrichDependencyLicenses_EmptyEntries" to match the regular expressio |
| 464 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %v" 5 times. |
| 484 | `go:S100` | MINOR | Rename function "TestEnrichDependencyLicenses_DefaultSwitchCase" to match the regular expr |
| 525 | `go:S100` | MINOR | Rename function "TestEnrichDependencyLicenses_NodeParseErrorNoRunErr" to match the regular |
| 561 | `go:S100` | MINOR | Rename function "TestEnrichDependencyLicenses_RustParseErrorNoRunErr" to match the regular |
| 593 | `go:S100` | MINOR | Rename function "TestParseRustLicenseMap_EmptyName" to match the regular expression ^(_\|[a |
| 608 | `go:S100` | MINOR | Rename function "TestParseRustLicenseMap_Truncation" to match the regular expression ^(_\|[ |
| 627 | `go:S100` | MINOR | Rename function "TestParseRustLicenseMap_EmptyLicense" to match the regular expression ^(_ |
| 656 | `go:S100` | MINOR | Rename function "TestNormalizeFoundLicense_UnknownSingleToken" to match the regular expres |
| 667 | `go:S100` | MINOR | Rename function "TestNormalizeFoundLicense_NAInput" to match the regular expression ^(_\|[a |
| 682 | `go:S100` | MINOR | Rename function "TestNormalizeFoundLicense_MultiToken" to match the regular expression ^(_ |
| 696 | `go:S100` | MINOR | Rename function "TestNormalizeFoundLicense_SingleNonUnknownToken" to match the regular exp |
| 703 | `go:S100` | MINOR | Rename function "TestLicenseClassification_Verdict" to match the regular expression ^(_\|[a |

### `internal/adapters/tools/mongo_tools.go` (2 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 82 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 176 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/mongo_tools_test.go` (6 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 32 | `go:S100` | MINOR | Rename function "TestMongoFindHandler_Success" to match the regular expression ^(_\|[a-zA-Z |
| 61 | `go:S100` | MINOR | Rename function "TestMongoFindHandler_DeniesDatabaseOutsideProfileScopes" to match the reg |
| 76 | `go:S100` | MINOR | Rename function "TestMongoAggregateHandler_Success" to match the regular expression ^(_\|[a |
| 104 | `go:S100` | MINOR | Rename function "TestMongoAggregateHandler_MapsExecutionErrors" to match the regular expre |
| 123 | `go:S100` | MINOR | Rename function "TestMongoHandlers_NamesAndLiveClientErrors" to match the regular expressi |
| 153 | `go:S100` | MINOR | Rename function "TestMongoHelpers_ProfileAndDatabasePolicies" to match the regular express |

### `internal/adapters/tools/nats_tools.go` (3 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 80 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 167 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 263 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/nats_tools_test.go` (19 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 42 | `go:S100` | MINOR | Rename function "TestNATSRequestHandler_Success" to match the regular expression ^(_\|[a-zA |
| 58 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "dev.nats" 13 times. |
| 71 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox.echo" 4 times. |
| 81 | `go:S100` | MINOR | Rename function "TestNATSRequestHandler_DeniesSubjectOutsideProfileScope" to match the reg |
| 94 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error code: %s" 4 times. |
| 98 | `go:S100` | MINOR | Rename function "TestNATSPublishHandler_Success" to match the regular expression ^(_\|[a-zA |
| 122 | `go:S100` | MINOR | Rename function "TestNATSPublishHandler_DeniesReadOnlyProfile" to match the regular expres |
| 143 | `go:S100` | MINOR | Rename function "TestNATSPublishHandler_ExecutionError" to match the regular expression ^( |
| 159 | `go:S100` | MINOR | Rename function "TestNATSSubscribePullHandler_Success" to match the regular expression ^(_ |
| 185 | `go:S100` | MINOR | Rename function "TestNATSSubscribePullHandler_ExecutionError" to match the regular express |
| 206 | `go:S100` | MINOR | Rename function "TestNATSHandlers_NamesAndLiveClientErrors" to match the regular expressio |
| 219 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "://bad-url" 3 times. |
| 222 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 287 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_IgnoresMetadataOverride" to match the regular  |
| 301 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_UsesServerEnv" to match the regular expression |
| 316 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_AllowlistRejectsDisallowedHost" to match the r |
| 326 | `go:S100` | MINOR | Rename function "TestClampInt_AllBranches" to match the regular expression ^(_\|[a-zA-Z0-9] |
| 345 | `go:S100` | MINOR | Rename function "TestNATSSubscribePullHandler_ValidationPaths" to match the regular expres |
| 373 | `go:S100` | MINOR | Rename function "TestResolveProfileEndpoint_AllowlistAllowsWildcardAndCIDR" to match the r |

### `internal/adapters/tools/path_test.go` (2 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 10 | `go:S100` | MINOR | Rename function "TestResolvePath_AllowsWithinWorkspace" to match the regular expression ^( |
| 23 | `go:S100` | MINOR | Rename function "TestResolvePath_DeniesTraversalAndAllowlist" to match the regular express |

### `internal/adapters/tools/quality_gate_tools_test.go` (4 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 12 | `go:S100` | MINOR | Rename function "TestQualityGateHandler_PassAndFail" to match the regular expression ^(_\|[ |
| 85 | `go:S100` | MINOR | Rename function "TestQualityGateHandler_InvalidArgs" to match the regular expression ^(_\|[ |
| 113 | `go:S100` | MINOR | Rename function "TestEvaluateQualityGate_AllRules" to match the regular expression ^(_\|[a- |
| 138 | `go:S100` | MINOR | Rename function "TestQualityGateSummary_AllCases" to match the regular expression ^(_\|[a-z |

### `internal/adapters/tools/rabbit_tools.go` (3 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 109 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 220 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 325 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/rabbit_tools_test.go` (12 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 41 | `go:S100` | MINOR | Rename function "TestRabbitConsumeHandler_Success" to match the regular expression ^(_\|[a- |
| 44 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox.jobs" 9 times. |
| 69 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected map output, got %#v" 3 tim |
| 79 | `go:S100` | MINOR | Rename function "TestRabbitConsumeHandler_DeniesQueueOutsideProfileScopes" to match the re |
| 90 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error code: %s" 4 times. |
| 94 | `go:S100` | MINOR | Rename function "TestRabbitPublishHandler_Success" to match the regular expression ^(_\|[a- |
| 124 | `go:S100` | MINOR | Rename function "TestRabbitPublishHandler_DeniesReadOnlyProfile" to match the regular expr |
| 142 | `go:S100` | MINOR | Rename function "TestRabbitPublishHandler_ExecutionError" to match the regular expression  |
| 158 | `go:S100` | MINOR | Rename function "TestRabbitQueueInfoHandler_Success" to match the regular expression ^(_\|[ |
| 185 | `go:S100` | MINOR | Rename function "TestRabbitQueueInfoHandler_MapsExecutionErrors" to match the regular expr |
| 204 | `go:S100` | MINOR | Rename function "TestRabbitHandlers_NamesAndLiveClientErrors" to match the regular express |
| 218 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "amqp://invalid:5672" 4 times. |

### `internal/adapters/tools/redis_tools.go` (7 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 104 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 202 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 296 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 408 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 492 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 564 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 683 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/redis_tools_test.go` (21 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 82 | `go:S100` | MINOR | Rename function "TestRedisGetHandler_Success" to match the regular expression ^(_\|[a-zA-Z0 |
| 85 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "sandbox:todo:1" 3 times. |
| 101 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected map output, got %#v" 5 tim |
| 108 | `go:S100` | MINOR | Rename function "TestRedisGetHandler_DeniesKeyOutsideProfileScopes" to match the regular e |
| 116 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected key policy denial" 3 times |
| 119 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error code: %s" 7 times. |
| 123 | `go:S100` | MINOR | Rename function "TestRedisScanHandler_Success" to match the regular expression ^(_\|[a-zA-Z |
| 149 | `go:S100` | MINOR | Rename function "TestRedisExistsHandler_MapsExecutionErrors" to match the regular expressi |
| 168 | `go:S100` | MINOR | Rename function "TestRedisSetHandler_Success" to match the regular expression ^(_\|[a-zA-Z0 |
| 202 | `go:S100` | MINOR | Rename function "TestRedisSetHandler_RequiresTTL" to match the regular expression ^(_\|[a-z |
| 221 | `go:S100` | MINOR | Rename function "TestRedisSetHandler_DeniesKeyOutsideProfileScopes" to match the regular e |
| 238 | `go:S100` | MINOR | Rename function "TestRedisSetHandler_DeniesReadOnlyProfile" to match the regular expressio |
| 260 | `go:S100` | MINOR | Rename function "TestRedisDelHandler_Success" to match the regular expression ^(_\|[a-zA-Z0 |
| 291 | `go:S100` | MINOR | Rename function "TestRedisDelHandler_DeniesKeyOutsideProfileScopes" to match the regular e |
| 308 | `go:S100` | MINOR | Rename function "TestRedisDelHandler_DeniesReadOnlyProfile" to match the regular expressio |
| 330 | `go:S100` | MINOR | Rename function "TestRedisHandlers_ConstructorsAndNames" to match the regular expression ^ |
| 354 | `go:S100` | MINOR | Rename function "TestRedisMGetHandler_SuccessAndTruncation" to match the regular expressio |
| 384 | `go:S100` | MINOR | Rename function "TestRedisTTLHandler_Statuses" to match the regular expression ^(_\|[a-zA-Z |
| 414 | `go:S100` | MINOR | Rename function "TestLiveRedisClientMethods_EndpointValidation" to match the regular expre |
| 433 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 441 | `go:S100` | MINOR | Rename function "TestRedisHelpers_ProfileResolutionAndValueCoercion" to match the regular  |

### `internal/adapters/tools/repo_analysis_tools.go` (5 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 150 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 231 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 308 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 426 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 533 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/repo_analysis_tools_test.go` (10 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 24 | `go:S100` | MINOR | Rename function "TestRepoTestFailuresSummaryHandler_WithProvidedOutput" to match the regul |
| 35 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error: %#v" 5 times. |
| 42 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected source: %#v" 3 times. |
| 53 | `go:S100` | MINOR | Rename function "TestRepoStacktraceSummaryHandler_WithProvidedOutput" to match the regular |
| 87 | `go:S100` | MINOR | Rename function "TestRepoTestFailuresSummaryHandler_RunsTestsWhenOutputMissing" to match t |
| 118 | `go:S100` | MINOR | Rename function "TestRepoChangedFilesHandler_WithProvidedOutput" to match the regular expr |
| 159 | `go:S100` | MINOR | Rename function "TestRepoSymbolSearchHandler_WithRunnerOutput" to match the regular expres |
| 170 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/workspace/repo" 4 times. |
| 199 | `go:S100` | MINOR | Rename function "TestRepoFindReferencesHandler_ExcludesDeclarations" to match the regular  |
| 257 | `go:S100` | MINOR | Rename function "TestRepoChangedFilesHandler_RunsGitWhenOutputMissing" to match the regula |

### `internal/adapters/tools/repo_tools.go` (3 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 68 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 117 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 202 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/repo_tools_test.go` (13 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 17 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "go.mod" 5 times. |
| 18 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "write go.mod failed: %v" 5 times. |
| 58 | `go:S100` | MINOR | Rename function "TestRepoRunTestsInvoke_GoModule" to match the regular expression ^(_\|[a-z |
| 60 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "module example.com/repo\n\ngo 1.23\ |
| 97 | `go:S100` | MINOR | Rename function "TestRepoDetectProjectTypeInvoke_GoModule" to match the regular expression |
| 116 | `go:S100` | MINOR | Rename function "TestRepoBuildInvoke_GoModule" to match the regular expression ^(_\|[a-zA-Z |
| 146 | `go:S100` | MINOR | Rename function "TestDetectBuildCommand_RustAndC" to match the regular expression ^(_\|[a-z |
| 160 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "main.c" 4 times. |
| 172 | `go:S100` | MINOR | Rename function "TestDetectProjectTypeFromWorkspace_Extended" to match the regular express |
| 198 | `go:S100` | MINOR | Rename function "TestFilterRepoExtraArgs_GoAllowAndDeny" to match the regular expression ^ |
| 215 | `go:S100` | MINOR | Rename function "TestRepoBuildInvoke_DeniesDisallowedExtraArgs" to match the regular expre |
| 235 | `go:S100` | MINOR | Rename function "TestRepoRunTestsInvoke_AllowsPythonKFlagPair" to match the regular expres |
| 252 | `go:S100` | MINOR | Rename function "TestRepoHandlers_ConstructorsAndNames" to match the regular expression ^( |

### `internal/adapters/tools/runner_test.go` (9 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 68 | `go:S100` | MINOR | Rename function "TestRunCommand_SuccessAndFailure" to match the regular expression ^(_\|[a- |
| 77 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected output: %q" 3 times. |
| 92 | `go:S100` | MINOR | Rename function "TestRunCommand_Timeout" to match the regular expression ^(_\|[a-zA-Z0-9]+) |
| 113 | `go:S100` | MINOR | Rename function "TestLocalCommandRunner_Run" to match the regular expression ^(_\|[a-zA-Z0- |
| 143 | `go:S100` | MINOR | Rename function "TestRoutingCommandRunner_UsesLocalByDefault" to match the regular express |
| 164 | `go:S100` | MINOR | Rename function "TestK8sCommandRunner_Run" to match the regular expression ^(_\|[a-zA-Z0-9] |
| 167 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "kubernetes.local" 3 times. |
| 202 | `go:S100` | MINOR | Rename function "TestK8sCommandRunner_RunExitError" to match the regular expression ^(_\|[a |
| 226 | `go:S100` | MINOR | Rename function "TestK8sCommandRunner_Timeout" to match the regular expression ^(_\|[a-zA-Z |

### `internal/adapters/tools/secrets_tools_test.go` (7 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 13 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_TruncatesFindings" to match the regular ex |
| 47 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_FallbackToGrepWhenRipgrepMissing" to match |
| 94 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_NoMatchesExitCodeOne" to match the regular |
| 110 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_InvalidArgs" to match the regular expressi |
| 118 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_InvalidPath" to match the regular expressi |
| 126 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_NonExitOneFailure" to match the regular ex |
| 138 | `go:S100` | MINOR | Rename function "TestSecurityScanSecretsHandler_ClampsMaxResults" to match the regular exp |

### `internal/adapters/tools/swe_helpers_test.go` (11 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 122 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "GPL-3.0" 3 times. |
| 143 | `go:S100` | MINOR | Rename function "TestIntFromAny_AllBranches" to match the regular expression ^(_\|[a-zA-Z0- |
| 175 | `go:S100` | MINOR | Rename function "TestFloatFromAny_AllBranches" to match the regular expression ^(_\|[a-zA-Z |
| 223 | `go:S100` | MINOR | Rename function "TestNormalizeSeverityThreshold_AllBranches" to match the regular expressi |
| 249 | `go:S100` | MINOR | Rename function "TestSeverityListForThreshold_AllBranches" to match the regular expression |
| 261 | `go:S100` | MINOR | Rename function "TestNormalizeFindingSeverity_AllBranches" to match the regular expression |
| 279 | `go:S100` | MINOR | Rename function "TestSecuritySeverityRank_AllBranches" to match the regular expression ^(_ |
| 288 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "not supported" 3 times. |
| 298 | `go:S100` | MINOR | Rename function "TestDetectProjectTypeOrError_NonErrNotExist" to match the regular express |
| 331 | `go:S100` | MINOR | Rename function "TestDetectProjectTypeOrError_ErrNotExist" to match the regular expression |
| 353 | `go:S100` | MINOR | Rename function "TestDetectProjectTypeOrError_Success" to match the regular expression ^(_ |

### `internal/adapters/tools/toolchain_tools.go` (6 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 94 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 140 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 225 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 275 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 328 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 404 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/adapters/tools/toolchain_tools_test.go` (10 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 15 | `go:S100` | MINOR | Rename function "TestRepoDetectToolchainInvoke_GoModule" to match the regular expression ^ |
| 36 | `go:S100` | MINOR | Rename function "TestRepoValidateInvoke_GoModule" to match the regular expression ^(_\|[a-z |
| 52 | `go:S100` | MINOR | Rename function "TestRepoTestAliasInvoke_GoModule" to match the regular expression ^(_\|[a- |
| 68 | `go:S100` | MINOR | Rename function "TestGoBuildInvoke_GoModule" to match the regular expression ^(_\|[a-zA-Z0- |
| 112 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected invalid argument code, got |
| 116 | `go:S100` | MINOR | Rename function "TestGoTestInvoke_WithCoverage" to match the regular expression ^(_\|[a-zA- |
| 184 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "main.c" 3 times. |
| 200 | `go:S100` | MINOR | Rename function "TestMapProjectTypeToToolchain_Extended" to match the regular expression ^ |
| 241 | `go:S100` | MINOR | Rename function "TestValidateCommandForProject_AllToolchains" to match the regular express |
| 276 | `go:S100` | MINOR | Rename function "TestToolchainHelpers_SanitizeOutputName" to match the regular expression  |

### `internal/adapters/workspace/kubernetes_manager_test.go` (12 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 19 | `go:S100` | MINOR | Rename function "TestKubernetesManager_CreateAndCloseSession" to match the regular express |
| 23 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-runtime" 9 times. |
| 40 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-1" 4 times. |
| 44 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-a" 5 times. |
| 67 | `go:S100` | MINOR | Rename function "TestKubernetesManager_CreateSessionRejectsSourceRepoPath" to match the re |
| 89 | `go:S3776` | CRITICAL | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| 89 | `go:S100` | MINOR | Rename function "TestKubernetesManager_SessionPodSecurityDefaultsAndGitSecret" to match th |
| 99 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "ws-session-1" 3 times. |
| 138 | `go:S100` | MINOR | Rename function "TestKubernetesManager_SessionPodUsesRunnerBundle" to match the regular ex |
| 143 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "registry.example.com/runner/toolcha |
| 162 | `go:S100` | MINOR | Rename function "TestKubernetesManager_SessionPodRejectsUnknownRunnerBundle" to match the  |
| 186 | `go:S100` | MINOR | Rename function "TestKubernetesManager_CloseSessionFindsPodByLabel" to match the regular e |

### `internal/adapters/workspace/kubernetes_pod_janitor_test.go` (11 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 44 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepDeletesTerminalContainerPods" to match the  |
| 49 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-runtime" 18 times. |
| 52 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "workspace-container-run" 3 times. |
| 70 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepDeletesOrphanedSessionPods" to match the re |
| 78 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "workspace-session" 3 times. |
| 99 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepKeepsFreshPodsDuringSessionGracePeriod" to  |
| 128 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepDeletesExpiredSessionsAndStoreKey" to match |
| 137 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-expired" 7 times. |
| 168 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepContainerPodsOrphanedSession" to match the  |
| 198 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepContainerPodsExpiredSession" to match the r |
| 234 | `go:S100` | MINOR | Rename function "TestKubernetesPodJanitor_SweepNilGuards" to match the regular expression  |

### `internal/adapters/workspace/local_manager_test.go` (4 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 14 | `go:S100` | MINOR | Rename function "TestLocalManager_CreateSessionFromSourcePath" to match the regular expres |
| 26 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-a" 3 times. |
| 52 | `go:S100` | MINOR | Rename function "TestLocalManager_CloseSessionRemovesWorkspace" to match the regular expre |
| 84 | `go:S100` | MINOR | Rename function "TestLocalManager_ExpiredSessionGetsEvicted" to match the regular expressi |

### `internal/app/invocation_store_memory_test.go` (2 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 11 | `go:S100` | MINOR | Rename function "TestInMemoryInvocationStore_SaveAndGet" to match the regular expression ^ |
| 37 | `go:S100` | MINOR | Rename function "TestInMemoryInvocationStore_GetMissing" to match the regular expression ^ |

### `internal/app/service_integration_test.go` (7 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 19 | `go:S100` | MINOR | Rename function "TestService_CreateAndListTools" to match the regular expression ^(_\|[a-zA |
| 24 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-a" 4 times. |
| 27 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected error creating session:  |
| 50 | `go:S100` | MINOR | Rename function "TestService_FsWriteRequiresApproval" to match the regular expression ^(_\| |
| 62 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "notes/todo.txt" 3 times. |
| 75 | `go:S100` | MINOR | Rename function "TestService_FsWriteAndRead" to match the regular expression ^(_\|[a-zA-Z0- |
| 114 | `go:S100` | MINOR | Rename function "TestService_PathTraversalDenied" to match the regular expression ^(_\|[a-z |

### `internal/app/service_schema_test.go` (3 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 8 | `go:S100` | MINOR | Rename function "TestValidateOutputAgainstSchema_AcceptsTypedArraySlices" to match the reg |
| 19 | `go:S100` | MINOR | Rename function "TestValidateOutputAgainstSchema_RejectsWrongType" to match the regular ex |
| 25 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/app/service_unit_test.go` (15 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 139 | `go:S1186` | CRITICAL | Add a nested comment explaining why this function is empty or complete the implementation. |
| 168 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-1" 14 times. |
| 325 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "unexpected list error: %v" 3 times. |
| 335 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "fs.list" 3 times. |
| 339 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "k8s.get_pods" 6 times. |
| 473 | `go:S100` | MINOR | Rename function "TestInvokeTool_DeniesWhenRateLimitExceeded" to match the regular expressi |
| 500 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "expected denied invocation status,  |
| 507 | `go:S100` | MINOR | Rename function "TestInvocationQuotaLimiter_DeniesWhenPrincipalRateLimitExceeded" to match |
| 540 | `go:S100` | MINOR | Rename function "TestInvokeTool_DeniesWhenConcurrencyLimitExceeded" to match the regular e |
| 605 | `go:S100` | MINOR | Rename function "TestInvokeTool_DeniesWhenOutputQuotaExceeded" to match the regular expres |
| 630 | `go:S100` | MINOR | Rename function "TestInvokeTool_DeniesWhenArtifactCountQuotaExceeded" to match the regular |
| 640 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "text/plain" 4 times. |
| 658 | `go:S100` | MINOR | Rename function "TestInvokeTool_DeniesWhenArtifactSizeQuotaExceeded" to match the regular  |
| 889 | `go:S100` | MINOR | Rename function "TestInvokeTool_DeduplicatesByCorrelationID" to match the regular expressi |
| 925 | `go:S100` | MINOR | Rename function "TestGetInvocation_HydratesOutputAndLogsFromArtifactRefs" to match the reg |

### `internal/app/session_store_memory_test.go` (3 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 11 | `go:S100` | MINOR | Rename function "TestInMemorySessionStore_SaveGetDelete" to match the regular expression ^ |
| 14 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "session-1" 4 times. |
| 46 | `go:S100` | MINOR | Rename function "TestInMemorySessionStore_ExpiresSession" to match the regular expression  |

### `internal/httpapi/auth_test.go` (13 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 9 | `go:S100` | MINOR | Rename function "TestAuthConfigFromEnv_DefaultPayload" to match the regular expression ^(_ |
| 22 | `go:S100` | MINOR | Rename function "TestAuthConfigFromEnv_TrustedHeadersRequiresToken" to match the regular e |
| 34 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "X-Workspace-Tenant-Id" 3 times. |
| 35 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "X-Workspace-Actor-Id" 3 times. |
| 36 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "X-Workspace-Roles" 3 times. |
| 37 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "X-Workspace-Auth-Token" 4 times. |
| 38 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "shared-token" 4 times. |
| 41 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/v1/sessions" 3 times. |
| 43 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-a" 3 times. |
| 44 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "actor-a" 3 times. |
| 59 | `go:S100` | MINOR | Rename function "TestAuthConfigFromEnv_CustomHeaders" to match the regular expression ^(_\| |
| 79 | `go:S100` | MINOR | Rename function "TestAuthConfigFromEnv_TrustedHeadersValid" to match the regular expressio |
| 92 | `go:S100` | MINOR | Rename function "TestAuthConfigFromEnv_UnsupportedMode" to match the regular expression ^( |

### `internal/httpapi/server.go` (2 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 68 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |
| 154 | `godre:S8193` | MINOR | Remove this unnecessary variable declaration and use the expression directly in the condit |

### `internal/httpapi/server_test.go` (20 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 22 | `go:S100` | MINOR | Rename function "TestHTTPAPI_EndToEndToolExecutionInWorkspace" to match the regular expres |
| 27 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-a" 5 times. |
| 34 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/v1/sessions" 10 times. |
| 53 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/v1/sessions/" 12 times. |
| 63 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/tools/fs.read_file/invoke" 5 times |
| 77 | `go:S100` | MINOR | Rename function "TestHTTPAPI_ApprovalRequiredAndRouteErrors" to match the regular expressi |
| 118 | `go:S100` | MINOR | Rename function "TestHTTPAPI_InvocationRoutesAndHealth" to match the regular expression ^( |
| 125 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/metrics" 3 times. |
| 150 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "seed.txt" 3 times. |
| 160 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/v1/invocations/" 7 times. |
| 199 | `go:S100` | MINOR | Rename function "TestHTTPAPI_TrustedHeadersUsesAuthenticatedPrincipal" to match the regula |
| 202 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "workspace-shared-token" 6 times. |
| 207 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "tenant-auth" 6 times. |
| 208 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "actor-auth" 4 times. |
| 232 | `go:S100` | MINOR | Rename function "TestHTTPAPI_TrustedHeadersRejectsMissingToken" to match the regular expre |
| 254 | `go:S100` | MINOR | Rename function "TestHTTPAPI_TrustedHeadersEnforcesSessionOwnership" to match the regular  |
| 275 | `go:S1192` | CRITICAL | Define a constant instead of duplicating this literal "/tools" 3 times. |
| 453 | `go:S100` | MINOR | Rename function "TestHTTPAPI_MethodNotAllowedEdgeCases" to match the regular expression ^( |
| 497 | `go:S100` | MINOR | Rename function "TestHTTPAPI_InvocationRouteEdgeCases" to match the regular expression ^(_ |
| 545 | `go:S100` | MINOR | Rename function "TestHTTPAPI_DecodeBodyNilBody" to match the regular expression ^(_\|[a-zA- |

### `runner-images/Dockerfile` (1 issues)

| Line | Rule | Severity | Message |
|------|------|----------|---------|
| 43 | `docker:S7031` | MINOR | Merge this RUN instruction with the consecutive ones. |
