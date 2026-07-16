# E2E Tests — Underpass Runtime

The end-to-end suite is a single **data-driven per-tool matrix**. One runner
(`tests/00-tool-matrix`) enumerates the runtime's tool catalog (~130 tools) and,
for every tool, runs an **authored adversarial spec** against a live runtime
deployment (gRPC over TLS, as a Kubernetes Job). It replaces the previous
scenario-based suite (health, session, invoke, policy, learning, agents…) —
those concerns are folded into the matrix's preamble and per-tool cases.

## Why per-tool

The runtime's value is **governed tool execution**: a small model must never
bypass input validation, approval, or path confinement, on any tool. The matrix
asserts that for every tool and stays in lockstep with the catalog — a new tool
is discovered automatically, and the goal is a spec for each.

## Adversarial specs — the source of truth

Each tool family has a spec at `tests/00-tool-matrix/specs/<family>.yaml`
(**31 files, 130 tools, 697 cases**). Unlike the catalog's `examples` — which
are partly non-executable placeholders — a spec carries args that really run
against the known fixture workspace, plus adversarial cases designed to surface
bugs, each with an **explicit expected outcome**. The engine
(`spec_runner.py`) runs every case and compares the actual result to the
expectation; a **divergence is a BUG**.

Every case declares a `category` (informational — all enforced identically):

| Category | Intent |
|----------|--------|
| `capability` | exercise one real mode/option of the tool's input schema |
| `adversarial` | malformed / oversized / conflicting / edge input meant to break it |
| `governance` | must be denied — traversal, missing approval, invalid args |

### Expectation grammar (`expect:` per case)

```yaml
- name: read_missing
  category: adversarial
  args: { path: "does-not-exist.xyz" }
  expect:
    status: failed              # succeeded | failed | denied  (invocation status)
    error_code: not_found       # exact error code when not succeeded
    # error_code_in: [a, b]     # any-of
    # output: { field: value }  # exact-match output fields
    # output_present: [field]   # fields that must exist
    # output_match: { f: "re" } # regex on a string field
    # skip_if_error_code: [connection_failed]  # treat as skip, not bug
```

A case may run `setup:` invocations first to establish state (e.g. seed a file
with `fs.write_file`). Cases share one session, so setup tools must be ones a
generic `developer,devops` session can run — use `fs.move` (not the
`platform_admin`-gated `fs.delete`) to stage/clean workspace files.

### Outcomes (honest, never flaky)

- `pass` — actual matched the expectation.
- `fail` — actual diverged from the expectation (a **BUG**; the only thing that
  fails CI).
- `skip` — the case needs a backend/profile that isn't provisioned
  (`connection_failed`, `no_profile`, …, or a case's own `skip_if_error_code`).
  Recorded, never a failure — the suite is honest on a partially-provisioned
  cluster.
- `gated` — the tool is registered but not visible to a generic session because
  it needs a `tool_profile`/role (k8s rollout/saturation, `notify`,
  `github.merge_pr`…). Correctly hidden; its invocation cases are skipped.

Tools without an authored spec fall back to a derived governance + happy-path
matrix (`invalid_input`, `approval_gate`, `policy_traversal`, `happy_path`) so
coverage never regresses. Every tool currently ships a spec.

Live-cluster baseline (2026-07-15, self-contained infra + the 10 bug fixes the
sweep surfaced): **757 pass / 0 fail / 12 gated / 20 skip**.

## Self-contained test infrastructure

The suite brings its own dependencies — no external services. `charts/e2e-infra`
(a Helm chart) stands up, in an isolated namespace, everything the matrix needs:

- **Backends** (`templates/backends.yaml`): redis, mongo, rabbitmq, kafka
  (KRaft, single node), prometheus, httpbin (target for `web.fetch` /
  `api.benchmark`), a notify sink.
- **Internal git server** — `e2e-gitfixture` serves the polyglot fixture repo
  (`e2e/fixtures/polyglot`: Go + Node + Python + Rust + Dockerfile) over
  `git://e2e-gitfixture:9418/polyglot`, so cloning needs no GitHub.
- **k8s targets** (`templates/k8s-targets.yaml`): real Deployments for the
  `k8s.*` tools to inspect and mutate inside the release namespace.
- **A dedicated test runtime** (subchart) wired to all backends via
  `WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON`, TLS disabled, with a "fat" runner
  image that carries the language toolchains plus trivy/syft/k6.

```bash
helm install e2e charts/e2e-infra -n underpass-e2e --create-namespace
# … run the matrix (below) … then:
helm uninstall e2e -n underpass-e2e
```

## Running

```bash
# Whole matrix against a runtime (build + push + deploy the Job, parse evidence)
./e2e/run-e2e-tests.sh --test 00

# Skip build/push if the image is already in the registry
./e2e/run-e2e-tests.sh --skip-build --skip-push --test 00
```

Against the self-contained chart, run the matrix as a Pod by rendering
`charts/e2e-infra/templates/e2e-test-job.yaml` (minus the Helm hook annotations)
and `kubectl apply` it in the release namespace. It clones the internal git
fixture and talks plain gRPC to the test runtime.

Image push needs `ghcr.io` auth and an `imagePullSecrets` named `ghcr-pull` in
the namespace. Against the base (mTLS) runtime, the Job connects over `https://`
with the client cert/CA from the `e2e-client-tls` secret.

### Local dry-run (no cluster)

Print the plan (authored spec per tool, or the derived fallback) without
contacting a runtime:

```bash
E2E_DRY_RUN=1 \
  CATALOG_PATH=internal/adapters/tools/catalog_defaults.yaml \
  SPECS_DIR=e2e/tests/00-tool-matrix/specs \
  python3 e2e/tests/00-tool-matrix/test_tool_matrix.py
```

## Authoring a spec

1. Read the tool's handler in `internal/adapters/tools/` — the real behavior is
   the contract (error codes, defaults, clamps, path fields), not the catalog
   text.
2. Add `- tool: <name>` with `cases:` to `specs/<family>.yaml`. Cover each input
   mode (`capability`), the edges that could break it (`adversarial`), and every
   deny path (`governance`).
3. Anchor expectations on real fixture state (the polyglot repo, the seeded
   files). Use `skip_if_error_code` for live backend reads so a transient error
   is a skip, not a false bug.
4. Validate with the local dry-run, then run against the cluster and triage any
   `fail` — either the spec expectation is wrong, or you found a real bug.

## Output

The runner prints a per-outcome summary and emits an evidence JSON
(`EVIDENCE_FILE`) with every `{tool, case, outcome, detail}` plus a roll-up. The
Job exits non-zero if any case `fail`s; `skip`/`gated` do not fail the run.

## Layout

```
e2e/
  tests/
    00-tool-matrix/
      test_tool_matrix.py   matrix runner (preamble + per-tool dispatch)
      spec_runner.py        adversarial spec engine (load + check_case)
      specs/<family>.yaml   authored specs — 31 files, 130 tools, 697 cases
      Dockerfile, job.yaml
    workspace_common/       shared harness (base.py — gRPC/TLS transport)
    e2e_tests.yaml          test registry
  fixtures/polyglot/        multi-language fixture repo (served by e2e-gitfixture)
charts/e2e-infra/           self-contained test infra (backends + git + runtime)
```
