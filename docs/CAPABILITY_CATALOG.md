# Workspace Capability Catalog

This file is generated from `internal/adapters/tools/DefaultCapabilities()`.
Do not edit manually. Regenerate with `make catalog-docs`.

- Total capabilities: `99`
- Families: `23`

## api.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `api.benchmark` | `external` | `medium` | `yes` | `reversible` | `best-effort` |

## artifact.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `artifact.download` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `artifact.list` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `artifact.upload` | `workspace` | `low` | `no` | `reversible` | `best-effort` |

## c.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `c.build` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `c.test` | `repo` | `medium` | `no` | `reversible` | `best-effort` |

## ci.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `ci.run_pipeline` | `repo` | `medium` | `no` | `reversible` | `best-effort` |

## conn.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `conn.describe_profile` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `conn.list_profiles` | `workspace` | `low` | `no` | `none` | `guaranteed` |

## container.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `container.exec` | `workspace` | `medium` | `yes` | `reversible` | `best-effort` |
| `container.logs` | `workspace` | `low` | `no` | `none` | `best-effort` |
| `container.ps` | `workspace` | `low` | `no` | `none` | `best-effort` |
| `container.run` | `workspace` | `medium` | `yes` | `reversible` | `best-effort` |

## fs.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `fs.copy` | `workspace` | `medium` | `no` | `reversible` | `best-effort` |
| `fs.delete` | `workspace` | `high` | `yes` | `irreversible` | `best-effort` |
| `fs.list` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `fs.mkdir` | `workspace` | `medium` | `no` | `reversible` | `best-effort` |
| `fs.move` | `workspace` | `medium` | `no` | `reversible` | `best-effort` |
| `fs.patch` | `workspace` | `medium` | `yes` | `reversible` | `best-effort` |
| `fs.read_file` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `fs.search` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `fs.stat` | `workspace` | `low` | `no` | `none` | `guaranteed` |
| `fs.write_file` | `workspace` | `medium` | `yes` | `reversible` | `best-effort` |

## git.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `git.apply_patch` | `repo` | `medium` | `yes` | `reversible` | `best-effort` |
| `git.branch_list` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `git.checkout` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `git.commit` | `repo` | `medium` | `yes` | `irreversible` | `none` |
| `git.diff` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `git.fetch` | `repo` | `medium` | `yes` | `reversible` | `best-effort` |
| `git.log` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `git.pull` | `repo` | `medium` | `yes` | `reversible` | `best-effort` |
| `git.push` | `repo` | `high` | `yes` | `irreversible` | `best-effort` |
| `git.show` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `git.status` | `repo` | `low` | `no` | `none` | `guaranteed` |

## go.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `go.build` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `go.generate` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `go.mod.tidy` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `go.test` | `repo` | `medium` | `no` | `reversible` | `best-effort` |

## image.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `image.build` | `repo` | `medium` | `yes` | `reversible` | `best-effort` |
| `image.inspect` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `image.push` | `repo` | `medium` | `yes` | `irreversible` | `best-effort` |

## k8s.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `k8s.apply_manifest` | `cluster` | `medium` | `yes` | `reversible` | `best-effort` |
| `k8s.get_deployments` | `cluster` | `low` | `no` | `none` | `guaranteed` |
| `k8s.get_images` | `cluster` | `low` | `no` | `none` | `guaranteed` |
| `k8s.get_logs` | `cluster` | `low` | `no` | `none` | `guaranteed` |
| `k8s.get_pods` | `cluster` | `low` | `no` | `none` | `guaranteed` |
| `k8s.get_services` | `cluster` | `low` | `no` | `none` | `guaranteed` |
| `k8s.restart_deployment` | `cluster` | `medium` | `yes` | `reversible` | `best-effort` |
| `k8s.rollout_status` | `cluster` | `medium` | `yes` | `none` | `guaranteed` |

## kafka.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `kafka.consume` | `external` | `medium` | `no` | `none` | `best-effort` |
| `kafka.produce` | `external` | `medium` | `yes` | `reversible` | `best-effort` |
| `kafka.topic_metadata` | `external` | `low` | `no` | `none` | `guaranteed` |

## mongo.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `mongo.aggregate` | `external` | `medium` | `no` | `none` | `best-effort` |
| `mongo.find` | `external` | `medium` | `no` | `none` | `best-effort` |

## nats.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `nats.publish` | `external` | `medium` | `yes` | `reversible` | `best-effort` |
| `nats.request` | `external` | `medium` | `no` | `reversible` | `best-effort` |
| `nats.subscribe_pull` | `external` | `medium` | `no` | `none` | `best-effort` |

## node.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `node.build` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `node.install` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `node.lint` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `node.test` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `node.typecheck` | `repo` | `medium` | `no` | `reversible` | `best-effort` |

## python.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `python.install_deps` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `python.test` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `python.validate` | `repo` | `low` | `no` | `none` | `guaranteed` |

## quality.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `quality.gate` | `repo` | `low` | `no` | `none` | `guaranteed` |

## rabbit.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `rabbit.consume` | `external` | `medium` | `no` | `none` | `best-effort` |
| `rabbit.publish` | `external` | `medium` | `yes` | `reversible` | `best-effort` |
| `rabbit.queue_info` | `external` | `low` | `no` | `none` | `guaranteed` |

## redis.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `redis.del` | `external` | `medium` | `yes` | `irreversible` | `best-effort` |
| `redis.exists` | `external` | `low` | `no` | `none` | `guaranteed` |
| `redis.get` | `external` | `low` | `no` | `none` | `guaranteed` |
| `redis.mget` | `external` | `low` | `no` | `none` | `guaranteed` |
| `redis.scan` | `external` | `low` | `no` | `none` | `best-effort` |
| `redis.set` | `external` | `medium` | `yes` | `reversible` | `best-effort` |
| `redis.ttl` | `external` | `low` | `no` | `none` | `guaranteed` |

## repo.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `repo.build` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `repo.changed_files` | `repo` | `low` | `no` | `none` | `best-effort` |
| `repo.coverage_report` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `repo.detect_project_type` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `repo.detect_toolchain` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `repo.find_references` | `repo` | `low` | `no` | `none` | `best-effort` |
| `repo.package` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `repo.run_tests` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `repo.stacktrace_summary` | `repo` | `low` | `no` | `reversible` | `best-effort` |
| `repo.static_analysis` | `repo` | `low` | `no` | `none` | `best-effort` |
| `repo.symbol_search` | `repo` | `low` | `no` | `none` | `best-effort` |
| `repo.test` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `repo.test_failures_summary` | `repo` | `low` | `no` | `reversible` | `best-effort` |
| `repo.validate` | `repo` | `medium` | `no` | `reversible` | `best-effort` |

## rust.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `rust.build` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `rust.clippy` | `repo` | `medium` | `no` | `reversible` | `best-effort` |
| `rust.format` | `repo` | `low` | `no` | `reversible` | `best-effort` |
| `rust.test` | `repo` | `medium` | `no` | `reversible` | `best-effort` |

## sbom.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `sbom.generate` | `repo` | `low` | `no` | `none` | `guaranteed` |

## security.*

| Tool | Scope | Risk | Approval | Side Effects | Idempotency |
| --- | --- | --- | --- | --- | --- |
| `security.license_check` | `repo` | `medium` | `no` | `none` | `guaranteed` |
| `security.scan_container` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `security.scan_dependencies` | `repo` | `low` | `no` | `none` | `guaranteed` |
| `security.scan_secrets` | `workspace` | `low` | `no` | `none` | `guaranteed` |

