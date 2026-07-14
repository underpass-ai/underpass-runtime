# E2E Tests — Underpass Runtime

The end-to-end suite is a single **data-driven per-tool matrix**. One runner
(`tests/00-tool-matrix`) loads the runtime tool catalog and, for every
registered tool (~130), derives and runs a set of cases against a live runtime
deployment (gRPC over TLS, as a Kubernetes Job). It replaces the previous
scenario-based suite (health, session, invoke, policy, learning, agents…) —
those concerns are now folded into the matrix's preamble and per-tool cases.

## Why per-tool

The runtime's value is **governed tool execution**: a small model must never be
able to bypass input validation, approval, or path confinement, on any tool.
The matrix asserts exactly that for every tool, and stays in lockstep with the
catalog — new tools are covered automatically, with no new test code.

## Cases per tool

Derived from each tool's catalog metadata (`input_schema`, `requires_approval`,
`policy.path_fields`, `examples`):

| Case | Applies to | Asserts |
|------|-----------|---------|
| `discovery` | all 130 | tool is registered and visible in the session catalog |
| `happy_path` | 110 (with an `example`) | invoke with the catalog's own example args |
| `invalid_input` | 71 (with required fields) | empty args are **rejected**, never executed |
| `approval_gate` | 32 (`requires_approval` + example) | invoking **without** approval is blocked |
| `policy_traversal` | 44 (with a `path_field`) | a workspace-escape path is **denied** |

That is ~387 cases across the catalog (avg ~3 per tool).

### Outcomes (honest, never flaky)

The three governance cases (`invalid_input`, `approval_gate`, `policy_traversal`)
are the hard backbone — deterministic for every visible tool, and the **only**
source of a `fail`. Everything else is informational:

- `pass` — a governance case held, or a visible tool's example executed
  (`succeeded`). Real coverage for the ~79 workspace-local tools on a seeded
  fixture (`fs`, `repo`, `git`, language toolchains…).
- `gated` — the tool is registered but not visible to a generic session because
  it needs a `tool_profile`/role (k8s rollout/saturation, `notify`,
  `github.merge_pr`…). Correctly hidden; its invocation cases are skipped.
- `executed` — a visible tool's example reached execution but didn't `succeed`
  (a role/policy restriction, a missing external dependency, or fixture state the
  example assumes). Not a failure — the suite stays green on a bare cluster.
- `fail` — a governance invariant was violated (empty args accepted, approval
  bypassed, or a workspace-escape path allowed). The only thing that fails CI.

Live-cluster baseline (2026-06-16): **130 tools, 358 cases, 0 fail** — 283 pass,
63 executed, 12 gated. Every `invalid_input`/`approval_gate`/`policy_traversal`
held across the catalog.

## Running

```bash
# Whole matrix (build + push + deploy the Job, parse evidence)
./e2e/run-e2e-tests.sh --test 00

# Skip build/push if the image is already in the registry
./e2e/run-e2e-tests.sh --skip-build --skip-push --test 00
```

Requires `ghcr.io` auth for image push and an `imagePullSecrets` named
`ghcr-pull` in the namespace. The Job connects over `https://` with the client
cert/CA mounted from the `e2e-client-tls` secret.

### Local dry-run (no cluster)

Validate the derived case matrix against the catalog without contacting a
runtime:

```bash
E2E_DRY_RUN=1 CATALOG_PATH=internal/adapters/tools/catalog_defaults.yaml \
  python3 e2e/tests/00-tool-matrix/test_tool_matrix.py
```

## Output

The runner prints a per-outcome summary and emits an evidence JSON
(`EVIDENCE_FILE`) with every `{tool, case, outcome, detail}` plus a roll-up. The
Job exits non-zero if any case `fail`s; `executed` outcomes do not fail the run.

## Layout

```
e2e/tests/
  00-tool-matrix/       the per-tool matrix runner (test_tool_matrix.py, Dockerfile, job.yaml)
  workspace_common/     shared harness (base.py — gRPC/TLS transport, session + invoke helpers)
  e2e_tests.yaml        test registry
```
