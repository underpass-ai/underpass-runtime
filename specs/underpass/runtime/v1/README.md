# Underpass Runtime Contract v1

This directory holds the versioned contract for `underpass-runtime`.

## Files

- `runtime.proto`
  Target synchronous gRPC contract for runtime session lifecycle, capability catalog, governed invocation, and health.
- `contract.md`
  Semantic contract for the synchronous API, including auth, error semantics, model definitions, and implementation status.
- `events.md`
  Canonical asynchronous NATS contract, including subjects, envelope, payloads, and emission semantics.

## Source of Truth

The runtime currently has two contract layers:

1. Synchronous control plane
   - Target contract: `specs/underpass/runtime/v1/runtime.proto`
   - Current served transport: `api/openapi/workspace.v1.yaml`
   - Current server implementation: `internal/httpapi/server.go`

2. Asynchronous domain events
   - Canonical event model: `internal/domain/events.go`
   - Canonical subject mapping: `internal/adapters/eventbus/nats_publisher.go`
   - Public event documentation: `events.md`
   - Machine-readable event documentation: `api/asyncapi/workspace-events.v1.yaml`

## Current Implementation Status

| Surface | Status |
| --- | --- |
| gRPC sync contract | Specified in `runtime.proto`, not yet served by a gRPC server |
| HTTP sync contract | Served today |
| NATS async contract | Published today |
| Prometheus metrics | Served today over HTTP only |

## Versioning

This directory is versioned.

Breaking changes require a new directory:

- `specs/underpass/runtime/v2/`

The sync and async contracts should stay aligned at the same version whenever possible.
