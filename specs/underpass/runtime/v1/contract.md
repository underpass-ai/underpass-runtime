# Underpass Runtime Sync Contract v1

This document describes the semantic contract for the synchronous runtime API.

It is intentionally transport-aware:

- the target transport is gRPC, defined in `runtime.proto`
- the current served transport is HTTP, defined in `api/openapi/workspace.v1.yaml`

The contract is the same product surface in both cases.

## Bounded Context

`underpass-runtime` is a governed execution runtime.

Its domain language is:

- session
- principal
- runtime target
- capability
- invocation
- artifact
- policy decision

It does not own:

- memory or rehydration
- demo orchestration
- incident semantics
- agent cognition

Those belong to other bounded contexts.

## Transport Split

### Sync

The sync surface is the control plane for:

- creating and closing sessions
- discovering and recommending tools
- invoking a tool
- retrieving invocation state, logs, and artifacts
- health checks

### Async

The async surface is the domain event stream emitted by the runtime.

It is documented separately in `events.md`.

### Metrics

Prometheus metrics remain on HTTP.

They are not part of the gRPC contract.

## Implementation Status

As of this version:

- `runtime.proto` defines the target gRPC shape
- the production server still serves HTTP in `internal/httpapi/server.go`
- the runtime does not yet expose a gRPC server

This is intentional documentation, not a hidden gap.

## Services

## SessionService

Responsibilities:

- create a workspace session
- close a workspace session

Methods:

- `CreateSession`
- `CloseSession`

### CreateSession semantics

Inputs:

- optional `session_id`
- `repo_url`
- `repo_ref`
- `source_repo_path`
- `allowed_paths`
- `principal`
- `metadata`
- `expires_in_seconds`

Behavior:

- `principal.tenant_id` is required in payload auth mode
- `principal.actor_id` is required in payload auth mode
- if `expires_in_seconds <= 0`, the service defaults to `3600`
- the workspace manager chooses the concrete runtime backend
- on success, the runtime emits `workspace.session.created`

Response:

- materialized `Session`

### CloseSession semantics

Inputs:

- `session_id`

Behavior:

- validates `session_id`
- closes the session through the workspace manager
- emits `workspace.session.closed` when the session existed

Response:

- `closed=true`

## CapabilityCatalogService

Responsibilities:

- list tools available in the session
- provide filtered discovery
- provide ranked recommendations

Methods:

- `ListTools`
- `DiscoverTools`
- `RecommendTools`

### ListTools semantics

The service returns only tools that are:

- supported by the session runtime
- allowed by policy for the session principal

Important implementation detail:

policy evaluation for `ListTools` uses empty args and `approved=true`.
This is an availability check, not an execution attempt.

### DiscoverTools semantics

`DiscoverTools` is the filtered view over `ListTools`.

Filter behavior:

- different filter fields are AND-combined
- each repeated value inside one field is OR-combined

Examples:

- `risk=[low,medium]` means low OR medium
- `risk=[low]` plus `cost=[cheap]` means low AND cheap

Discovery detail:

- `compact` returns LLM-optimized summaries
- `full` returns documentation-grade tool metadata plus optional telemetry stats

### RecommendTools semantics

Recommendations are derived from:

- static capability metadata
- task hint lexical matching
- telemetry boosts and penalties when telemetry is available

Current scoring inputs include:

- risk
- side effects
- approval requirement
- cost hint or derived cost
- task hint token matches
- success rate
- deny rate
- p95 duration

Target traceability additions:

- every recommendation response returns a stable `recommendation_id`
- every recommendation response returns the `event_id` and `event_subject` of
  the emitted decision fact
- every recommendation response declares `decision_source`,
  `algorithm_id`, `algorithm_version`, and `policy_mode`
- the same `recommendation_id` must resolve in the
  `underpass.runtime.learning.v1` evidence plane

These additions are intentionally additive. They do not change the ranking
semantics of `RecommendTools`; they make those semantics auditable.

## InvocationService

Responsibilities:

- governed tool execution
- retrieval of invocation state
- retrieval of logs
- retrieval of artifacts

Methods:

- `InvokeTool`
- `GetInvocation`
- `GetInvocationLogs`
- `GetInvocationArtifacts`

### InvokeTool semantics

Inputs:

- `session_id`
- `tool_name`
- optional `correlation_id`
- `args`
- `approved`

Behavior:

1. validate session exists
2. validate tool exists
3. deduplicate by `(session_id, tool_name, correlation_id)` when correlation ID is present
4. create a running invocation record
5. emit `workspace.invocation.started`
6. authorize the invocation
7. enforce runtime support constraints
8. enforce quotas and concurrency limits
9. execute the tool
10. persist output, logs, and artifacts
11. validate output against the tool output schema
12. materialize final invocation state
13. emit either:
   - `workspace.invocation.denied`
   - `workspace.invocation.completed`

### Governed denials vs transport errors

This contract distinguishes two classes of failure.

Request or infrastructure failures:

- invalid argument
- unauthenticated
- permission denied for session access
- not found
- internal failure

These should map to transport status.

Governed execution outcomes:

- policy denied
- approval required
- runtime unsupported
- quota denied
- execution failed
- timeout

These should be represented as a materialized `Invocation` with:

- `status=DENIED` or `status=FAILED`
- populated `error`

The response should preserve the invocation record instead of collapsing everything into a transport error.

### Correlation ID semantics

When `correlation_id` is set:

- the runtime may return an existing invocation for the same `(session, tool, correlation_id)`
- this is the idempotency mechanism for the public API

### Runtime support gating

Not every tool is supported on every runtime backend.

Current implementation rules include:

- non-cluster tools are generally available
- cluster-scoped tools require Kubernetes runtime
- some Kubernetes delivery tools are additionally gated by feature flag

### Output validation

After execution, the runtime validates the output against the capability output schema.

Current implementation validates:

- top-level type
- required object fields
- simple property types for object properties

If validation fails, the invocation is materialized as failed.

### Artifact persistence

The runtime persists:

- tool-defined artifacts
- invocation output as `invocation-output.json` when output exists
- invocation logs as `invocation-logs.jsonl` when logs exist

The invocation may carry either:

- inlined output and logs
- or references (`output_ref`, `logs_ref`) that can be hydrated later

## HealthService

Responsibilities:

- liveness check only

Methods:

- `Check`

Metrics are deliberately excluded from the gRPC contract.

## Canonical Models

## Principal

Fields:

- `tenant_id`
- `actor_id`
- `roles[]`

## Session

Fields:

- `id`
- `workspace_path`
- `runtime`
- `repo_url`
- `repo_ref`
- `allowed_paths[]`
- `principal`
- `metadata`
- `created_at`
- `expires_at`

## RuntimeRef

Fields:

- `kind`
- `namespace`
- `pod_name`
- `container`
- `container_id`
- `workdir`

## Tool

The full tool model is intentionally rich enough to support discovery full without extra lookups.

Fields:

- identity and description
- input and output schema
- scope, side effects, risk, approval, idempotency
- constraints
- preconditions
- postconditions
- policy metadata
- observability metadata
- examples

### PolicyMetadata

For parity with the current runtime model, `PolicyMetadata` should cover:

- `path_fields`
- `arg_fields`
- `profile_fields`
- `subject_fields`
- `topic_fields`
- `queue_fields`
- `key_prefix_fields`
- `namespace_fields`
- `registry_fields`

This keeps the contract aligned with the existing capability registry and policy enforcement model.

### Observability

Capability observability is defined by the public runtime contract that we provide.

At `v1`, the observability shape is:

- `trace_name`
- `span_name`

This is the source of truth for clients and generated SDKs.

If the implementation diverges temporarily during migration, the implementation must converge to the contract, not the other way around.

If logging-specific hints are needed later, they should be added explicitly in a new contract revision or compatible field extension, rather than replacing these two fields implicitly.

### Examples

Examples should be documented as UTF-8 encoded JSON values if they remain byte-encoded in the proto.

## Invocation

Fields:

- `id`
- `session_id`
- `tool_name`
- `correlation_id`
- `status`
- `started_at`
- `completed_at`
- `duration_ms`
- `trace_name`
- `span_name`
- `exit_code`
- `output`
- `output_ref`
- `logs`
- `logs_ref`
- `artifacts`
- `error`

Important:

- `output` must allow any JSON value, not only objects

## Artifact

Fields:

- `id`
- `name`
- `path`
- `content_type`
- `size_bytes`
- `sha256`
- `created_at`

## Error

This is a governed execution error, not a transport error.

Fields:

- `code`
- `message`
- `retryable`

## Auth

The runtime supports two auth modes.

## Payload mode

The caller provides `principal` in `CreateSession`.

This is the default mode.

## Trusted headers / metadata mode

The caller provides authenticated identity through request metadata.

Canonical keys:

- `x-workspace-auth-token`
- `x-workspace-tenant-id`
- `x-workspace-actor-id`
- `x-workspace-roles`

Rules:

- the shared token must match
- `tenant_id` and `actor_id` are required
- roles are comma-separated and deduplicated
- session and invocation access are checked against authenticated principal identity

## Versioning

Breaking changes require `v2`.

Examples of breaking changes:

- changing service names
- changing field meaning
- changing governed failure semantics
- changing event subject scheme
- narrowing the allowed JSON shape of `Invocation.output`
