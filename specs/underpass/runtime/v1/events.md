# Underpass Runtime Async Event Contract v1

This document describes the canonical asynchronous contract emitted by `underpass-runtime`.

It complements the synchronous control plane in `runtime.proto`.

## Current Status

The runtime publishes domain events today through NATS JetStream.

Current implementation references:

- event envelope and payloads: `internal/domain/events.go`
- subject mapping: `internal/adapters/eventbus/nats_publisher.go`

## Subject Model

There are two related names for each event:

1. event type
   - semantic event name inside the envelope
   - example: `workspace.session.created`

2. NATS subject
   - routing subject used on the bus
   - example: `workspace.events.session.created`

This distinction is intentional.

## Canonical Subject Prefix

The canonical subject prefix is:

```text
workspace.events
```

The default stream subject filter is:

```text
workspace.events.>
```

Default stream name:

```text
WORKSPACE_EVENTS
```

## Envelope

All events use the same JSON envelope:

```json
{
  "id": "evt-abc123",
  "type": "workspace.invocation.completed",
  "version": "v1",
  "timestamp": "2026-03-31T12:00:00Z",
  "session_id": "session-123",
  "tenant_id": "acme",
  "actor_id": "agent-1",
  "payload": {}
}
```

Fields:

| Field | Description |
| --- | --- |
| `id` | Unique event ID. Used as message deduplication key in JetStream. |
| `type` | Semantic event type, without the `.events` routing prefix. |
| `version` | Event schema version. Current value: `v1`. |
| `timestamp` | RFC3339 timestamp generated at event creation time. |
| `session_id` | Session that owns the event. |
| `tenant_id` | Tenant identity from the session principal. |
| `actor_id` | Actor identity from the session principal. |
| `payload` | Event-type-specific JSON payload. |

## Event Catalog

## 1. Session Created

Subject:

```text
workspace.events.session.created
```

Type:

```text
workspace.session.created
```

Payload:

- `runtime_kind`
- `repo_url`
- `repo_ref`
- `expires_at`
- `workspace_dir`

Emission point:

- after successful session creation

## 2. Session Closed

Subject:

```text
workspace.events.session.closed
```

Type:

```text
workspace.session.closed
```

Payload:

- `runtime_kind`
- `duration_sec`
- `invocation_count`

Emission point:

- after session close when the session existed

Note:

`invocation_count` is currently optional and may be omitted.

## 3. Invocation Started

Subject:

```text
workspace.events.invocation.started
```

Type:

```text
workspace.invocation.started
```

Payload:

- `invocation_id`
- `tool_name`
- `correlation_id`

Emission point:

- after the running invocation record is stored
- before authorization and execution complete

## 4. Invocation Completed

Subject:

```text
workspace.events.invocation.completed
```

Type:

```text
workspace.invocation.completed
```

Payload:

- `invocation_id`
- `tool_name`
- `correlation_id`
- `status`
- `exit_code`
- `duration_ms`
- `output_bytes`
- `artifact_count`
- `error_code`

Emission point:

- after a failed or succeeded invocation is materialized

Important:

- this event covers both `SUCCEEDED` and `FAILED`
- `DENIED` has its own event

## 5. Invocation Denied

Subject:

```text
workspace.events.invocation.denied
```

Type:

```text
workspace.invocation.denied
```

Payload:

- `invocation_id`
- `tool_name`
- `correlation_id`
- `reason`

Emission point:

- after a governed denial is materialized

Current denial causes include:

- policy denial
- approval required
- runtime unsupported
- rate limit denial
- concurrency denial
- output or artifact quota denial

## 6. Artifact Stored

Subject:

```text
workspace.events.artifact.stored
```

Type:

```text
workspace.artifact.stored
```

Payload:

- `invocation_id`
- `artifact_id`
- `name`
- `content_type`
- `size_bytes`
- `sha256`

Status:

- defined in the domain model
- defined in the public event documentation
- not emitted by the current service implementation yet

This is a reserved event in `v1`, not a currently published one.

## Emission Matrix

| Event | Defined in model | Documented | Emitted today |
| --- | --- | --- | --- |
| `workspace.session.created` | yes | yes | yes |
| `workspace.session.closed` | yes | yes | yes |
| `workspace.invocation.started` | yes | yes | yes |
| `workspace.invocation.completed` | yes | yes | yes |
| `workspace.invocation.denied` | yes | yes | yes |
| `workspace.artifact.stored` | yes | yes | no |

## Subject Mapping Rule

Subject generation rule in the current implementation is:

```text
workspace.events + "." + suffix(type after the first dot)
```

Examples:

- `workspace.session.created` -> `workspace.events.session.created`
- `workspace.invocation.completed` -> `workspace.events.invocation.completed`

## Ordering and Delivery

The event bus is best-effort from the caller perspective.

Key behaviors:

- event publishing must not block the primary runtime operation
- the event ID is used as JetStream deduplication key
- outbox-backed publication may be enabled through configuration

The contract does not guarantee global ordering across sessions.

Consumers should key their logic by:

- `session_id`
- `invocation_id`
- `timestamp`
- `id`

## Versioning

Breaking changes require `v2`.

Examples:

- changing the subject prefix
- changing the envelope fields
- renaming event types
- changing payload meaning
- starting to emit `artifact.stored` with a different payload than documented here
