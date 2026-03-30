# ADR-001: Hexagonal Architecture in Go

**Status**: Accepted
**Date**: 2025-12-01
**Deciders**: Tirso (architect)

## Context

underpass-runtime executes tools inside isolated workspaces on behalf of AI
agents. The service must support multiple infrastructure backends (local,
Docker, Kubernetes) for workspaces, multiple persistence backends (memory,
Valkey) for sessions and invocations, and multiple event transports (none,
NATS). Adding a new backend should not require changes to business logic.

Go does not have a standard framework for hexagonal architecture. The ecosystem
favours explicit interfaces and manual dependency injection over DI containers
or annotation-driven frameworks.

## Decision

Adopt hexagonal architecture (ports and adapters) with the following package
layout:

```
internal/
├── domain/          Pure value objects and domain events. No dependencies.
├── app/             Business logic (Service) + port interfaces (types.go).
├── adapters/        Infrastructure implementations of ports.
│   ├── tools/       96+ tool implementations (Invoker port).
│   ├── workspace/   WorkspaceManager: local, docker, kubernetes.
│   ├── policy/      Authorizer: static policy engine.
│   ├── sessionstore/    SessionStore: memory, valkey.
│   ├── invocationstore/ InvocationStore: memory, valkey.
│   ├── storage/     ArtifactStore: local, s3.
│   ├── eventbus/    EventPublisher: noop, nats.
│   ├── audit/       AuditLogger: structured slog.
│   └── telemetry/   TelemetryRecorder: noop, memory, valkey.
├── httpapi/         HTTP transport (driving adapter).
├── bootstrap/       Composition root — wires ports to adapters.
└── tlsutil/         TLS configuration shared across adapters.
```

Port interfaces are defined in `internal/app/types.go` and kept minimal
(1-3 methods each). Business logic in `app/service.go` depends only on
interfaces, never on concrete adapters.

Dependency injection is manual: `bootstrap/` reads environment variables and
constructs the concrete adapter graph. No DI container or code generation.

## Consequences

**Positive:**
- Adding a new backend (e.g., PostgreSQL session store) requires only a new
  adapter package implementing the existing interface. Zero changes to `app/`.
- Unit tests use hand-written fakes (no gomock). Fakes implement the same
  port interface, keeping tests fast and infrastructure-free.
- Build tags (`k8s`) exclude Kubernetes-specific adapters from non-K8s builds,
  reducing binary size and attack surface.

**Negative:**
- `bootstrap/` is verbose — each backend requires explicit wiring code.
- Some duplication across adapter packages (e.g., Valkey connection setup
  repeated in sessionstore, invocationstore, telemetry). Accepted trade-off:
  each adapter owns its connection lifecycle independently.
- `app/service.go` is the largest file (~1,100 lines). It concentrates all
  business orchestration. Splitting it would fragment the domain workflow.

## Alternatives Considered

1. **Clean Architecture with separate `usecase/` package**: Rejected. In Go,
   the extra layer adds indirection without benefit when there is a single
   entry point (the HTTP API). The `app` package already plays the use-case
   role.

2. **DI framework (Wire, Fx)**: Rejected. Manual wiring in `bootstrap/` is
   explicit and debuggable. The project has ~10 ports — not enough to justify
   code generation overhead.

3. **Flat package structure**: Rejected. As the tool catalog grew to 96+
   capabilities, a flat layout would make navigation impossible. The hexagonal
   split keeps adapter code isolated and independently testable.
