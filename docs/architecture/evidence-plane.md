# Evidence Plane

## What exists today

Every `RecommendTools` call produces auditable evidence:

### 1. Bridge fields in gRPC response

```json
{
  "recommendations": [...],
  "recommendation_id": "rec-ce557598c511be67",
  "event_id": "evt-55733a389bd1668d",
  "event_subject": "runtime.learning.recommendation.emitted",
  "decision_source": "learned_policy_thompson",
  "algorithm_id": "beta_thompson_sampling",
  "algorithm_version": "1.0.0",
  "policy_mode": "shadow"
}
```

### 2. Persisted RecommendationDecision

Query via gRPC:

```
LearningEvidenceService.GetRecommendationDecision(recommendation_id)
```

Returns: recommendation_id, session_id, tenant_id, actor_id, task_hint,
top_k, context_signature, decision_source, algorithm_id, algorithm_version,
policy_mode, candidate_count, ranked recommendations with scores, event
linkage.

### 3. Evidence bundle

Query via gRPC:

```
LearningEvidenceService.GetEvidenceBundle(recommendation_id)
```

Returns: the RecommendationDecision wrapped in a bundle. In the current
implementation, only the recommendation field is populated. Future phases
will add policy, run, aggregate, and event lineage fields.

### 4. NATS event

Subject: `workspace.events.learning.recommendation.emitted`

Payload: `RecommendationEmittedPayload` with recommendation_id, task_hint,
top_k, decision_source, algorithm_id, algorithm_version, policy_mode,
and ranked tool list.

Published to JetStream stream `WORKSPACE_EVENTS` with deduplication via
event ID.

## Tool-learning pipeline events

Every CronJob run emits:

| Event | Subject | When |
|-------|---------|------|
| Run started | `tool_learning.run.started` | Pipeline begins |
| Policy computed | `tool_learning.policy.computed` | Per policy batch |
| Snapshot published | `tool_learning.snapshot.published` | After S3 audit write |
| Run completed | `tool_learning.run.completed` | Pipeline succeeds |
| Run failed | `tool_learning.run.failed` | Pipeline errors |
| Policy updated | `tool_learning.policy.updated` | After Valkey write (legacy) |

## What is specified but not yet implemented

The `specs/underpass/runtime/learning/v1/learning.proto` defines 14 RPCs.
Only 2 are implemented today:

| RPC | Status |
|-----|--------|
| GetRecommendationDecision | **Implemented** |
| GetEvidenceBundle | **Implemented** (partial — recommendation only) |
| GetLearningStatus | Specified |
| ListEventFacts | Specified |
| GetEventFact | Specified |
| ListRecommendationEvents | Specified |
| GetDiscoverySnapshot | Specified |
| ListDiscoveryEvents | Specified |
| ListPolicies | Specified |
| GetPolicy | Specified |
| ListPolicyRuns | Specified |
| GetPolicyRun | Specified |
| ListPolicyRunEvents | Specified |
| GetAggregate | Specified |

The remaining RPCs are the target for the evidence projection layer —
read models built from the NATS event stream.
