# Underpass Runtime Learning Async Event Contract v1

This document describes the canonical asynchronous contract for learning and
recommendation evidence in `underpass-runtime`.

It complements:

- `specs/underpass/runtime/v1/events.proto`
- `specs/underpass/runtime/learning/v1/learning.proto`

## Current Status

The runtime ecosystem already publishes asynchronous facts today.

Current evidence:

- runtime workspace events are already public
- `tool_learning.policy.updated` is already published by `tool-learning`
- the events in this document define the target catalog required for strong
  recommendation auditability

## Subject Model

There are two families of subjects:

1. runtime learning facts
   - prefix: `runtime.learning`

2. tool-learning batch facts
   - prefix: `tool_learning`

These subjects are intentionally separate:

- runtime owns online discovery and recommendation facts
- `tool-learning` owns offline policy computation facts

## Canonical Subjects

- `runtime.learning.discovery.recorded`
- `runtime.learning.recommendation.emitted`
- `runtime.learning.recommendation.accepted`
- `runtime.learning.recommendation.rejected`
- `tool_learning.run.started`
- `tool_learning.run.completed`
- `tool_learning.run.failed`
- `tool_learning.policy.computed`
- `tool_learning.policy.updated`
- `tool_learning.policy.degraded`
- `tool_learning.snapshot.published`

## Envelope

All learning events should use a common JSON envelope with:

- `id`
- `type`
- `version`
- `timestamp`
- `session_id`
- `tenant_id`
- `actor_id`
- `correlation_id`
- `causation_id`
- `payload`

The event envelope is the primary causal proof for the evidence plane.

## Event Catalog

## 1. Discovery Recorded

Subject:

```text
runtime.learning.discovery.recorded
```

Purpose:

- materialize that a discovery query happened
- persist stable evidence for demo and audit

Payload:

- `discovery_id`
- `detail`
- `total_tools`
- `filtered_tools`
- `tool_ids`

## 2. Recommendation Emitted

Subject:

```text
runtime.learning.recommendation.emitted
```

Purpose:

- record the exact decision emitted to the client
- bind a stable `recommendation_id` to the ranked list

Payload:

- `recommendation_id`
- `task_hint`
- `top_k`
- `decision_source`
- `algorithm_id`
- `algorithm_version`
- `policy_mode`
- `tools[]`

## 3. Recommendation Accepted

Subject:

```text
runtime.learning.recommendation.accepted
```

Purpose:

- record explicit uptake of a recommendation

Payload:

- `recommendation_id`
- `selected_tool_id`

## 4. Recommendation Rejected

Subject:

```text
runtime.learning.recommendation.rejected
```

Purpose:

- record explicit rejection or override

Payload:

- `recommendation_id`
- `reason`

## 5. Policy Run Started

Subject:

```text
tool_learning.run.started
```

Payload:

- `run_id`
- `schedule`
- `algorithm_id`
- `algorithm_version`
- `feature_schema_version`
- `window`

## 6. Policy Run Completed

Subject:

```text
tool_learning.run.completed
```

Payload:

- `run_id`
- `aggregates_read`
- `policies_written`
- `policies_filtered`
- `snapshot_ref`
- `duration_ms`

## 7. Policy Run Failed

Subject:

```text
tool_learning.run.failed
```

Payload:

- `run_id`
- `error_code`
- `message`
- `duration_ms`

## 8. Policy Computed

Subject:

```text
tool_learning.policy.computed
```

Payload:

- `policy_id`
- `run_id`
- `context_signature`
- `tool_id`
- `algorithm_id`
- `algorithm_version`
- `confidence`
- `snapshot_ref`

## 9. Policy Updated

Subject:

```text
tool_learning.policy.updated
```

Payload:

- `policy_id`
- `run_id`
- `context_signature`
- `tool_id`
- `store_ref`

## 10. Policy Degraded

Subject:

```text
tool_learning.policy.degraded
```

Payload:

- `policy_id`
- `context_signature`
- `tool_id`
- `reason`

## 11. Snapshot Published

Subject:

```text
tool_learning.snapshot.published
```

Payload:

- `run_id`
- `snapshot_ref`
- `checksum`

## Causality Rules

The contract expects:

- recommendation events to carry `correlation_id`
- offline policy events to carry causal references where applicable
- evidence APIs to expose these IDs back to clients

This is required to connect:

- discovery
- recommendation
- invocation
- policy runs
- policy updates

without relying on logs.
