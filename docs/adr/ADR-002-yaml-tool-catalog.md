# ADR-002: YAML-Driven Tool Catalog

**Status**: Accepted
**Date**: 2025-12-15
**Deciders**: Tirso (architect)

## Context

underpass-runtime exposes 96+ governed tools across 15 families (fs, git, repo,
container, image, security, k8s, ci, api, messaging, data, artifact, build,
test, coverage). Each tool carries rich metadata:

- `scope` (session / global)
- `side_effects` (none / local / external)
- `risk_level` (low / medium / high / critical)
- `requires_approval` (boolean)
- `idempotency` (idempotent / non_idempotent)
- `cost_hint` (free / low / medium / high)
- `tags` (list of classification tags)
- `args_schema` (JSON Schema for input validation)
- `output_schema` (JSON Schema for output validation)

This metadata drives the policy engine, tool discovery, Thompson Sampling
recommendations, and documentation generation.

## Decision

Define all tool metadata in a single embedded YAML file:
`internal/adapters/tools/catalog_defaults.yaml` (~116 KB).

The YAML is embedded at compile time via `//go:embed` and parsed once on first
access behind a `sync.Once`. The catalog is the **source of truth** for all
tool metadata. Go code registers tool implementations against catalog entries
by name.

Startup panics if:
- A registered tool name has no matching YAML entry.
- A YAML entry has empty required fields (name, description, scope, risk_level).

Documentation is auto-generated from the same YAML via `cmd/catalog-docs/`,
producing `docs/capability-catalog.md`.

## Consequences

**Positive:**
- Single source of truth for tool metadata — no drift between code, docs,
  and policy configuration.
- Non-engineers (product, security) can review and modify tool risk levels,
  approval requirements, and tags without touching Go code.
- Tool discovery API returns metadata directly from the catalog, ensuring
  agents always see accurate capability descriptions.
- Thompson Sampling and policy decisions use the same metadata, preventing
  inconsistencies.
- Auto-generated docs (`make catalog-docs`) guarantee documentation is always
  current.

**Negative:**
- 116 KB YAML file is large. Navigation requires search rather than browsing.
- Startup validation via `panic()` is harsh. A misconfigured deployment
  crashes immediately rather than degrading gracefully. This is intentional:
  a tool catalog error means agents would invoke tools with wrong metadata,
  which is worse than downtime.
- YAML lacks the type safety of Go structs for schema definitions. Mitigated
  by comprehensive startup validation.

## Alternatives Considered

1. **Tool metadata in Go code** (struct literals): Rejected. 96 tools with
   full metadata would create enormous Go files. Reviewability by non-engineers
   is lost.

2. **Database-backed catalog** (PostgreSQL, Valkey): Rejected. Adds an
   infrastructure dependency for a read-only dataset that changes only at
   deploy time. Embedded YAML has zero runtime dependencies.

3. **OpenAPI / JSON Schema per tool**: Rejected. Individual schema files would
   scatter metadata across 96+ files. A single YAML keeps all tools in one
   reviewable document.

4. **Protocol Buffers**: Rejected. The catalog is not a wire protocol — it is
   a configuration artifact. YAML is more readable for the intended audience
   (product and security reviewers).
