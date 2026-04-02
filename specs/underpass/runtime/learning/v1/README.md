# Underpass Runtime Learning Contract v1

This directory holds the versioned contract for learning evidence,
recommendation traceability, and auditability in `underpass-runtime`.

## Files

- `learning.proto`
  Target synchronous read-only gRPC contract for evidence retrieval.
- `events.proto`
  Target asynchronous event contract for learning-related facts.
- `contract.md`
  Semantic contract for the read-only evidence API.
- `events.md`
  Semantic contract for async learning events.

## Source of Truth

The learning surface has two contract layers:

1. Synchronous evidence plane
   - Target contract: `specs/underpass/runtime/learning/v1/learning.proto`
   - Target HTTP shape: `api/openapi/learning.v1.yaml`

2. Asynchronous learning facts
   - Target event contract: `specs/underpass/runtime/learning/v1/events.proto`
   - Target AsyncAPI shape: `api/asyncapi/learning-events.v1.yaml`

These contracts complement, not replace:

- `specs/underpass/runtime/v1/runtime.proto`
- `specs/underpass/runtime/v1/events.proto`

The runtime control plane still owns discovery, recommendation requests, and
invocation. The learning plane owns evidence, lineage, and auditability.

## Current Implementation Status

| Surface | Status |
| --- | --- |
| gRPC learning evidence contract | Specified here, not yet served |
| HTTP learning evidence contract | Specified here, not yet served |
| Learning event contract | Partially implemented today (`tool_learning.policy.updated`), otherwise target |
| Runtime recommendation evidence linkage | Targeted through additive `recommendation_id` fields in `runtime.proto` |

## Versioning

This directory is versioned.

Breaking changes require a new directory:

- `specs/underpass/runtime/learning/v2/`

The sync and async learning contracts should stay aligned at the same version
whenever possible.
