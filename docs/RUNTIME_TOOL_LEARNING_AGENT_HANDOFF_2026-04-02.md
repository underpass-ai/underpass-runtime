# Runtime Tool Learning Agent Handoff

Date: 2026-04-02

Scope: recommendation evidence, tool learning integration, and auditability in
`underpass-runtime`

## Context

This note is for the agent working in `underpass-runtime`.

It is a stable-handoff summary, not a replacement for re-reading the touched
files before editing. Another agent may be modifying the repo in parallel.

The architectural intent is now explicit:

- `underpass-runtime` is event-driven
- NATS events are the primary facts
- the API is a read-only query and reconstruction plane over those facts
- demo and product must consume the same evidence surface

The quality bar is `A`, not “good enough for demo”.

That means:

- additive contracts over accidental shortcuts
- strong causal linkage
- no fake observability
- no claim of SOTA without a real active path

## Canonical Documents

Read these first:

- `docs/RUNTIME_TOOL_LEARNING_AUDIT.md`
- `docs/RUNTIME_TOOL_LEARNING_TRACEABILITY_API.md`
- `specs/underpass/runtime/learning/v1/README.md`
- `specs/underpass/runtime/learning/v1/contract.md`
- `specs/underpass/runtime/learning/v1/learning.proto`
- `specs/underpass/runtime/learning/v1/events.proto`
- `api/openapi/learning.v1.yaml`
- `api/asyncapi/learning-events.v1.yaml`

Also note the additive bridge already captured in:

- `specs/underpass/runtime/v1/runtime.proto`
- `specs/underpass/runtime/v1/contract.md`

## Non-Negotiable Architectural Invariants

### 1. Event-first

Do not build this as a polling API over mutable caches.

Required shape:

1. runtime emits recommendation facts
2. tool-learning emits run and policy facts
3. evidence state is projected from those facts
4. API reads the projection and returns lineage

### 2. Causality must be first-class

Every auditable resource must carry:

- `event_id`
- `event_subject`
- `correlation_id`
- `causation_id`
- `trace_id`
- `span_id`

If a resource cannot tell you which facts created it, it is not acceptable.

### 3. Runtime control plane and learning plane stay separated

`RecommendTools` stays an execution-facing API.

The learning evidence plane is read-only and separate:

- it explains the decision
- it does not become the decision path itself

### 4. Do not over-claim SOTA

The current active path is not yet SOTA.

Until the runtime online path consumes learned contextual policies and/or a
contextual bandit in production, docs and demo must not describe it that way.

## Stable Findings

### 1. The runtime online path still does not consume learned policies

Current state:

- `RecommendTools` is still heuristic plus telemetry
- there is no proven online consumption of `tool_policy:*`
- there is no proven subscription path that reacts to
  `tool_learning.policy.updated`

Implication:

- evidence and observability work should not pretend the online scorer is already
  policy-driven

### 2. The recommendation path lacks a durable decision fact

The missing product object is:

- `RecommendationDecision`

Without it:

- no stable `recommendation_id`
- no strong evidence bundle
- no clean bridge from agent API to audit API

### 3. The current event catalog is insufficient for tool-learning traceability

Existing workspace events are useful but not enough.

The missing facts are:

- `runtime.learning.discovery.recorded`
- `runtime.learning.recommendation.emitted`
- `runtime.learning.recommendation.accepted`
- `runtime.learning.recommendation.rejected`
- `tool_learning.run.started`
- `tool_learning.run.completed`
- `tool_learning.run.failed`
- `tool_learning.policy.computed`
- `tool_learning.policy.degraded`
- `tool_learning.snapshot.published`

### 4. The telemetry-to-learning loop is not closed end-to-end

Current evidence shows:

- runtime records telemetry
- tool-learning reads from a parquet lake
- the exporter or bridge between both is not clearly implemented in product code

Implication:

- do not present the loop as closed until the bridge is real and observable

### 5. Context is not yet carried strongly enough in the active runtime path

Important contextual fields exist in types and research code, but the hot path
does not yet populate and use them consistently.

This is a blocker for:

- contextual policy application
- credible contextual bandits
- high-quality tool discovery differentiation

## What Has Already Been Prepared

The contract groundwork is already in place:

- a new `underpass.runtime.learning.v1` spec tree
- HTTP and AsyncAPI targets for the evidence plane
- additive bridge fields in `RecommendToolsResponse`

This means the next work should be implementation, not more architecture churn.

## Recommended Execution Order

### P0

1. Emit `runtime.learning.recommendation.emitted`
2. Materialize and persist `RecommendationDecision`
3. Return `recommendation_id`, `event_id`, `event_subject`,
   `decision_source`, `algorithm_id`, `algorithm_version`, and `policy_mode`
   from `RecommendTools`
4. Expose `GetRecommendationDecision`
5. Expose `GetEvidenceBundle`

Why first:

- it closes the minimum bridge from runtime API to auditable evidence
- it gives the demo a truthful object to render

### P1

1. Emit `tool_learning.run.started`
2. Emit `tool_learning.run.completed|failed`
3. Emit `tool_learning.policy.computed`
4. Emit `tool_learning.snapshot.published`
5. Build read models for policy run lineage

Why second:

- it makes offline learning auditable as a first-class event stream

### P2

1. Wire policy read path into runtime online recommendation flow
2. Add `context_signature` production in the hot path
3. Expose active policy evidence in recommendation responses and evidence API
4. Add invalidation or refresh on `tool_learning.policy.updated`

Why third:

- this is where the product starts to become a real closed learning loop

### P3

1. Introduce a production contextual algorithm path
2. Make algorithm selection explicit and observable
3. Preserve backward-compatible evidence contracts

Why fourth:

- only after P0-P2 can you claim advanced recommendation credibly

## Acceptance Criteria

### For P0

- `RecommendTools` returns a stable `recommendation_id`
- each recommendation response maps to one persisted `RecommendationDecision`
- one recommendation response emits one `runtime.learning.recommendation.emitted`
  fact
- `GetRecommendationDecision(recommendation_id)` returns algorithm metadata and
  event linkage
- `GetEvidenceBundle(recommendation_id)` returns at least:
  - decision
  - event lineage
  - policy reference when present

### For P1

- every `tool-learning` run emits start and terminal facts
- policy computation has an auditable fact per computed policy or batch
- a run can be reconstructed by API without scraping logs

### For P2

- runtime recommendation behavior changes when learned policy changes
- that change is explainable through API
- policy freshness and source are visible

### For P3

- the active scorer is not heuristic-only
- algorithm choice is visible in the evidence plane
- evidence captures algorithm-specific reasoning data

## Validation Checklist

### Unit tests

- recommendation emission persists `RecommendationDecision`
- recommendation response includes bridge fields
- recommendation evidence lookup works by `recommendation_id`
- policy run projection tolerates duplicate at-least-once event delivery
- evidence lineage preserves `correlation_id` and `causation_id`

### Integration checks

- subscribe to `runtime.learning.>`
- subscribe to `tool_learning.>`
- request one recommendation
- verify emitted event and returned `recommendation_id` match
- run one tool-learning job
- verify run and policy events are emitted
- query evidence API and confirm reconstruction matches the bus facts

### Demo checks

- the demo can render recommendation ID
- the demo can render algorithm and source
- the demo can render lineage without reading raw logs
- the demo does not claim learned online policy if the runtime is still using
  heuristic ranking

## Anti-Patterns To Avoid

- do not build “evidence” only in the UI
- do not use Valkey as the sole proof that a decision happened
- do not expose a learning API that cannot reconstruct events
- do not silently collapse different decision sources into one label
- do not merge batch concerns into the `tool-learning` CronJob HTTP surface
- do not claim SOTA while the active path remains heuristic-only

## Preferred Short-Term Implementation Strategy

If you need the narrowest serious slice, implement this exact set:

1. runtime emits `runtime.learning.recommendation.emitted`
2. runtime stores `RecommendationDecision`
3. runtime serves `GetRecommendationDecision`
4. runtime serves `GetEvidenceBundle`
5. tool-learning emits `run.completed` and `policy.computed`

That is the smallest slice that is:

- honest
- demoable
- auditable
- aligned with the architecture

## Final Note

Do not optimize this work for local convenience.

Optimize it for a future in which:

- operators need proof
- product needs a credible differentiator
- auditors need lineage
- another team consumes the contract without tribal knowledge

If the implementation cannot explain why a tool was recommended, by which
algorithm, from which facts, then the feature is not finished.
