# Tool Catalog — Maintenance Guide

The workspace tool catalog lives in a single YAML file loaded at startup
via `go:embed`. This document explains the structure and how to add or
modify capabilities.

## File layout

```
internal/adapters/tools/
  catalog_defaults.yaml   # Source of truth — all 99 capabilities
  catalog_defaults.go     # go:embed loader → []domain.Capability
```

`DefaultCapabilities()` returns the parsed capabilities. Its signature is
unchanged — callers (main.go, tests, doc generator) are not affected.

## YAML structure

```yaml
# Reusable data via YAML anchors (ignored by the Go struct, resolved by parser)
_anchors:
  shell_deny_chars: &shell_deny [";", "|", "&", ...]
  lang_tool_output: &lang_tool_output '{"type":"object",...}'
  policy_extra_args_test: &policy_extra_args_test
    arg_fields:
      - field: extra_args
        ...

# Capability list
capabilities:
  - name: fs.list
    description: List files/directories under the session workspace.
    input_schema: '{"type":"object",...}'
    output_schema: '{"type":"object",...}'
    scope: workspace          # repo | workspace | cluster | external
    side_effects: none        # none | reversible | irreversible
    risk_level: low           # low | medium | high
    requires_approval: false  # omit when false (default)
    idempotency: guaranteed   # guaranteed | best-effort | none
    constraints:
      timeout_seconds: 15
      output_limit_kb: 256
      # max_retries: 0        # omit when 0 (default)
    preconditions:
      - path must be inside allowed_paths
    postconditions:
      - no file system mutation
    cost_hint: low
    policy:
      path_fields:
        - field: path
          workspace_relative: true
    examples:
      - '{"path":".","recursive":false}'
```

### Auto-derived fields

The loader sets these automatically — do **not** add them to the YAML:

| Field | Value |
|-------|-------|
| `observability.trace_name` | Always `workspace.tools` |
| `observability.span_name` | Equals the capability `name` |

### YAML anchors

Shared data (deny-char lists, output schemas, policy templates) is defined
in `_anchors` and referenced with `*anchor_name`. The YAML parser resolves
these before the Go structs are populated.

Available anchors:

| Anchor | Usage |
|--------|-------|
| `*shell_deny` | Shell injection deny characters (no space) |
| `*shell_deny_space` | Shell injection deny characters (with space) |
| `*test_allowed` | Allowed CLI prefixes for test tools |
| `*test_denied` | Denied CLI prefixes for test/build tools |
| `*build_allowed` | Allowed CLI prefixes for build tools |
| `*lang_tool_output` | Shared output schema for language toolchain tools |
| `*policy_extra_args_test` | Policy for `extra_args` with test prefixes |
| `*policy_extra_args_build` | Policy for `extra_args` with build prefixes |
| `*policy_git_remote` | Policy for git remote/refspec fields |

## Adding a new capability

1. **Edit `catalog_defaults.yaml`** — append a new entry under `capabilities:`:

```yaml
  - name: myns.my_tool
    description: Short description of what this tool does.
    input_schema: '{"type":"object","properties":{"arg1":{"type":"string"}},"required":["arg1"]}'
    output_schema: '{"type":"object","properties":{"result":{"type":"string"}}}'
    scope: workspace
    side_effects: none
    risk_level: low
    idempotency: guaranteed
    constraints:
      timeout_seconds: 30
      output_limit_kb: 256
    preconditions:
      - describe when this tool can run
    postconditions:
      - describe what side effects may occur
    cost_hint: low
    examples:
      - '{"arg1":"example_value"}'
```

2. **Validate** — run the catalog tests:

```bash
cd services/workspace
go test ./internal/adapters/tools/ -run TestDefaultCapabilities
```

   The loader validates all JSON schemas at startup. Invalid JSON will
   cause a panic with the capability name and field.

3. **Update docs** — regenerate the markdown catalog:

```bash
make catalog-docs
```

4. **Add to existence check** (optional) — if the capability is critical,
   add its name to the `seen` map in `catalog_defaults_test.go`.

5. **Implement the handler** — wire the tool's `Invoke` logic in the
   appropriate `*_tools.go` file under `internal/adapters/tools/`.

## Modifying an existing capability

Edit the entry directly in `catalog_defaults.yaml`. Run tests and
`make catalog-docs` afterward.

## Policy field reference

| Policy section | Purpose |
|----------------|---------|
| `path_fields` | Fields containing workspace-relative paths (validated by path policy) |
| `arg_fields` | Fields with constrained CLI args (allowed/denied prefixes, deny chars) |
| `profile_fields` | Connection profile ID fields |
| `subject_fields` | NATS subject fields |
| `topic_fields` | Kafka topic fields |
| `queue_fields` | RabbitMQ queue fields |
| `key_prefix_fields` | Redis key prefix fields |
| `namespace_fields` | Kubernetes namespace fields |
| `registry_fields` | Container registry fields |

## Why YAML instead of Go structs?

The previous `catalog_defaults.go` was 2272 lines of repetitive Go struct
literals with 18.82% code duplication (SonarCloud). The data is purely
declarative — no logic. YAML eliminates:

- Struct boilerplate per capability (~20 lines → ~15 lines)
- Factory functions (`newGoCapability`, `newRustCapability`, etc.)
- Repeated `TraceName`/`SpanName` (auto-derived)
- SonarCloud duplication (YAML is not analyzed as Go source)

The loader is 115 lines of Go. All existing tests and callers are unchanged.
