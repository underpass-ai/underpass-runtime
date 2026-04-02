# Underpass Runtime Learning Evidence Contract v1

This document describes the semantic contract for the learning evidence plane in
`underpass-runtime`.

It is intentionally transport-aware:

- the target gRPC surface is defined in `learning.proto`
- the target HTTP surface is defined in `api/openapi/learning.v1.yaml`

This contract is read-only by design.

## Bounded Context

`underpass-runtime` remains the owner of:

- session lifecycle
- tool discovery and recommendation requests
- governed invocation
- runtime domain events

The learning evidence plane owns:

- recommendation evidence
- discovery evidence
- policy evidence
- policy run evidence
- aggregate evidence
- event lineage and causal reconstruction

It does not own:

- tool execution
- session creation or closure
- policy training itself
- direct writes to recommendation outcomes

Those belong to the runtime control plane and to `tool-learning`.

## Architectural Posture

This contract assumes an event-driven architecture.

Principles:

- NATS events are the primary facts
- the evidence API is a query plane over those facts
- snapshots and caches are materializations, not the only source of truth
- all auditable resources must carry causal references

The expected semantics are:

- emission is at-least-once
- consumers are idempotent
- read models are eventually consistent
- snapshots are immutable and versioned

## Services

## LearningEvidenceService

Responsibilities:

- expose evidence for a recommendation decision
- expose discovery evidence
- expose policy evidence
- expose policy run evidence
- expose aggregates and evidence bundles
- expose event facts and event lineage

Methods:

- `GetLearningStatus`
- `ListEventFacts`
- `GetEventFact`
- `GetRecommendationDecision`
- `ListRecommendationEvents`
- `GetDiscoverySnapshot`
- `ListDiscoveryEvents`
- `ListPolicies`
- `GetPolicy`
- `ListPolicyRuns`
- `GetPolicyRun`
- `ListPolicyRunEvents`
- `GetAggregate`
- `GetEvidenceBundle`

### GetRecommendationDecision semantics

Inputs:

- `recommendation_id`

Behavior:

- returns the persisted evidence object for a recommendation
- includes algorithm, version, policy mode, and event references
- includes ranked recommendations with score breakdown
- must resolve the same `recommendation_id` returned by `RecommendTools`

### ListEventFacts semantics

Inputs:

- subject or type filters
- session, correlation, or causation filters
- time window
- pagination

Behavior:

- returns canonical learning-related facts
- includes both runtime learning events and `tool-learning` events
- does not expose transport-specific delivery metadata as the primary contract

### GetPolicy semantics

Inputs:

- `context_signature`
- `tool_id`

Behavior:

- returns the active policy evidence for that context/tool pair
- includes algorithm metadata, freshness, run linkage, and event linkage

### GetPolicyRun semantics

Inputs:

- `run_id`

Behavior:

- returns the auditable execution of a `tool-learning` run
- includes inputs, outputs, snapshot refs, and event refs

### GetEvidenceBundle semantics

Inputs:

- `recommendation_id`

Behavior:

- returns a compact but complete audit package suitable for demo or debugging
- joins decision, policy, run, aggregates, and event lineage

## Resource Requirements

Every auditable resource must provide:

1. stable identity
2. algorithm and version
3. causal references
4. snapshot or artifact references when relevant
5. role-safe redaction without losing lineage

Minimum causal fields:

- `event_id`
- `event_subject`
- `correlation_id`
- `causation_id`
- `trace_id`
- `span_id`

## Bridge to the Agent API

This contract depends on one additive change in the runtime control plane:

- `RecommendTools` must return `recommendation_id`

Recommended additive fields in `RecommendToolsResponse`:

- `recommendation_id`
- `event_id`
- `event_subject`
- `decision_source`
- `algorithm_id`
- `algorithm_version`
- `policy_mode`

Without that bridge, the evidence plane exists but the agent call cannot resolve
it cleanly.

## Security

The contract must support at least two visibility levels:

### Agent or client

Can access:

- recommendation outcome
- short explanations
- policy mode

Must not require:

- raw feature vectors
- snapshot URIs
- RNG traces

### Operator or auditor

Can access:

- lineage
- event references
- snapshot refs
- aggregate details
- algorithm parameters

Sensitive values should be redacted structurally, not by omitting the entire
resource.

## Implementation Status

As of this version:

- the contract is specified
- `tool_learning.policy.updated` already exists as a published event
- the recommendation evidence plane is not yet implemented
- recommendation responses do not yet expose the full linkage consistently

This is an intentional target contract, not a claim of current implementation.
