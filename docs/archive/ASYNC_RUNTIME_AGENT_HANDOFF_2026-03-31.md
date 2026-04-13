# Async Runtime Handoff

Date: 2026-03-31

Scope: asynchronous runtime surface in `underpass-runtime`

## Context

This note is for the agent working in `underpass-runtime`.

It is a snapshot-based review. Another agent may be editing the repo in parallel, so every item below should be revalidated against the current file before patching.

The goal is to isolate the gaps that still look stable from the current codebase:

- event contract vs emitted payload
- event-bus startup behavior
- outbox safety
- async documentation drift

## Stable Findings

### 1. `workspace.invocation.completed` promises `error_code` but the publisher does not emit it

Contract:

- `internal/domain/events.go`
  `InvocationCompletedPayload` includes `ErrorCode`
- `api/asyncapi/workspace-events.v1.yaml`
  `InvocationCompletedPayload.error_code`
- `specs/underpass/runtime/v1/events.md`
  lists `error_code` in the payload

Implementation:

- `internal/app/service.go`
  `publishInvocationCompleted()` populates:
  - `invocation_id`
  - `tool_name`
  - `correlation_id`
  - `status`
  - `exit_code`
  - `duration_ms`
  - `output_bytes`
  - `artifact_count`

It does not populate `error_code`.

Impact:

- async consumers lose the most useful diagnostic field for failed invocations
- contract and implementation are inconsistent

Recommended fix:

- derive `error_code` from `inv.Error.Code` when present
- include it in `InvocationCompletedPayload`

Acceptance:

- failing invocation emits `workspace.invocation.completed` with `payload.error_code`
- success path leaves `error_code` omitted
- unit test covers both cases

### 2. `EVENT_BUS=nats` degrades silently to `noop`

Implementation:

- `cmd/workspace/main.go`
  `buildEventBus()` returns `NoopPublisher` when NATS connection fails
- `cmd/workspace/main_test.go`
  `TestBuildEventBus_NATSFallbackToNoop`
  `TestBuildEventBus_UnknownValue`

Impact:

- when the operator explicitly asks for NATS, the runtime can boot without any event stream
- this undermines the event-driven claim and hides operational failure

Recommended fix:

- keep `EVENT_BUS=none` as the explicit noop mode
- when `EVENT_BUS=nats` is set and initialization fails, fail fast
- when `EVENT_BUS` has an unknown value, fail fast with configuration error

Acceptance:

- `EVENT_BUS=nats` + unreachable NATS => process exits non-zero
- `EVENT_BUS=kafka` => process exits non-zero
- default unset behavior can still remain noop if that is the intended local-dev default

### 3. `EVENT_BUS_OUTBOX=true` degrades silently when Valkey is unavailable

Implementation:

- `cmd/workspace/main.go`
  `buildOutboxRelay()` logs a warning and returns `nil, nil`
- `cmd/workspace/main_test.go`
  `TestBuildOutboxRelay_EnabledNoValkey`
  `TestBuildOutboxRelay_EnabledHostPort`

Impact:

- the operator can enable outbox and still run without it
- delivery guarantees become environment-dependent without a hard signal

Recommended fix:

- when `EVENT_BUS_OUTBOX=true`, Valkey initialization failure should be fatal
- if a degraded mode is desired, add an explicit setting for it instead of silent fallback

Acceptance:

- `EVENT_BUS_OUTBOX=true` + unreachable Valkey => process exits non-zero
- tests reflect fail-fast behavior

### 4. Outbox is not safe for multi-replica deployment

Implementation:

- `internal/adapters/eventbus/outbox.go`
  `Drain()` uses `LRange` without claiming messages
- `internal/adapters/eventbus/outbox.go`
  `Ack()` later removes the head with `LTrim`
- `internal/adapters/eventbus/outbox_relay.go`
  relay publishes first and acks after

Impact:

- two relays can drain the same batch concurrently
- both can publish before either trims the list
- JetStream dedup helps downstream, but the outbox itself does not provide safe distributed ownership

Decision needed:

- either enforce singleton relay
- or introduce claim/lease semantics
- or move to a queue/stream model that supports consumer ownership explicitly

Recommended short-term fix:

- document and enforce singleton relay if multi-replica safety is not being built now

Recommended long-term fix:

- add atomic claim semantics, lease token, or dedicated processing list

Acceptance:

- deployment mode is explicit
- if singleton-only, startup or Helm values make that constraint obvious
- if multi-replica is supported, relay tests prove no duplicate forward from concurrent relays

### 5. A poison message can wedge the outbox relay indefinitely

Implementation:

- `internal/adapters/eventbus/outbox.go`
  `Drain()` aborts on first JSON unmarshal failure
- `internal/adapters/eventbus/outbox_relay.go`
  relay retries the same batch forever

Impact:

- a single corrupt list item can block all later events

Recommended fix:

- introduce a poison-path:
  - skip malformed item with logging and metric
  - or move malformed entry to a dead-letter key
  - or quarantine the raw payload before continuing

Acceptance:

- one malformed entry does not permanently block later well-formed events
- there is an observable signal for dropped/quarantined events

### 6. AsyncAPI for `invocation.completed` is looser than actual behavior

Implementation:

- `internal/domain/invocation.go`
  status enum includes `running`, `succeeded`, `failed`, `denied`
- `internal/app/service.go`
  `denyInvocation()` emits `workspace.invocation.denied`
- `internal/app/service.go`
  `publishInvocationCompleted()` is used for completed execution paths

Contract:

- `api/asyncapi/workspace-events.v1.yaml`
  `InvocationCompletedPayload.status` currently allows:
  - `running`
  - `succeeded`
  - `failed`
  - `denied`

Impact:

- consumers must accept states that this event should not carry
- contract is weaker than behavior

Recommended fix:

- tighten `InvocationCompletedPayload.status` to:
  - `succeeded`
  - `failed`

Acceptance:

- AsyncAPI and semantic docs both match emitted behavior

### 7. Versioned sync docs still say gRPC is not served

Documentation:

- `specs/underpass/runtime/v1/README.md`
- `specs/underpass/runtime/v1/contract.md`

Implementation:

- `cmd/workspace/main.go`
  registers:
  - `SessionService`
  - `CapabilityCatalogService`
  - `InvocationService`
  - `HealthService`
  and starts gRPC server

Impact:

- current docs understate actual implementation status
- they are now a real source of confusion for anyone integrating against gRPC

Recommended fix:

- update versioned docs to say:
  - gRPC sync contract is served
  - HTTP sync contract is also served
  - Prometheus remains HTTP-only

Acceptance:

- versioned docs match the current runtime startup path

## Lower-Priority Notes

### `workspace.session.closed` and `invocation_count`

`SessionClosedPayload` includes `invocation_count`, but current emission omits it.

This is not the highest-priority gap because `specs/underpass/runtime/v1/events.md` already says the field is optional and may be omitted.

### `workspace.artifact.stored`

The event type and subject mapping exist, but there is no publish site in the current service.

This should stay documented as reserved in `v1` unless implementation starts emitting it.

## Suggested Execution Order

### P0

1. emit `error_code` in `workspace.invocation.completed`
2. fail fast when `EVENT_BUS=nats` is explicitly requested and cannot initialize
3. fail fast when `EVENT_BUS_OUTBOX=true` is explicitly requested and Valkey cannot initialize
4. tighten AsyncAPI status enum for `invocation.completed`
5. fix gRPC implementation-status docs

### P1

1. add poison-message handling for outbox relay
2. decide whether outbox is singleton-only or multi-replica safe

### P2

1. if multi-replica is required, redesign outbox claiming semantics
2. only after that, market the outbox as delivery-hardening rather than best-effort buffering

## Validation Checklist

### Unit tests

- failing invocation publishes `error_code`
- successful invocation omits `error_code`
- `EVENT_BUS=nats` init failure is fatal
- `EVENT_BUS_OUTBOX=true` init failure is fatal
- malformed outbox record does not wedge the relay forever
- `invocation.completed` contract only accepts `succeeded|failed`

### Integration checks

- subscribe to `workspace.events.>`
- run one succeeded invocation
- run one failed invocation
- run one denied invocation
- confirm subjects and payloads match:
  - `workspace.events.invocation.completed`
  - `workspace.events.invocation.denied`

### Documentation updates

- `specs/underpass/runtime/v1/README.md`
- `specs/underpass/runtime/v1/contract.md`
- `specs/underpass/runtime/v1/events.md`
- `api/asyncapi/workspace-events.v1.yaml`

## Final note

Do not trust this note more than the current code.

It is intentionally a stable-handoff summary, not a replacement for re-reading the touched files before editing.
