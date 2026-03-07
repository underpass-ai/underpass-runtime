# Workspace E2E Images

These Dockerfiles are owned by the `services/workspace` module because they
define the runtime/tooling surface required by workspace-oriented E2E tests.

Test directories under `e2e/tests/*` keep only test logic (`job.yaml`,
scripts, fixtures, assertions). Image build targets in each test `Makefile`
point to these Dockerfiles.

Current migrated images:

- `15-workspace-vllm-tool-orchestration.Dockerfile`
- `16-workspace-vllm-go-todo-evolution.Dockerfile`
- `19-workspace-vllm-node-todo-evolution.Dockerfile`
- `20-workspace-vllm-c-todo-evolution.Dockerfile`
