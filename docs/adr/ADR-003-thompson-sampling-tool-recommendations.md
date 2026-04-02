# ADR-003: Adaptive Tool Recommendations

**Status**: Accepted, **Updated 2026-04-02** (P0-P4 implemented)
**Date**: 2026-01-10 (original), 2026-04-02 (update)
**Deciders**: Tirso (architect)

## Context

Agents working through underpass-runtime have access to 96+ tools. Naive
approaches (alphabetical listing, static ranking) waste agent tokens on
irrelevant tools and delay task completion. The system needs an adaptive
recommendation mechanism that:

1. Learns from historical invocation outcomes (success, failure, latency, cost).
2. Balances exploration (trying less-used tools) with exploitation (recommending
   proven tools).
3. Operates without requiring the agent to have prior knowledge of tool
   effectiveness.
4. Respects hard constraints (policy, risk level, cost limits).

## Decision

Implement Thompson Sampling with Beta-Binomial posteriors as the tool
recommendation engine. The system has two components:

### 1. Online Heuristic (in `internal/app/recommender.go`)

The workspace service provides immediate recommendations using a heuristic
scoring model:

- **Task hint matching**: tools whose tags or descriptions match the agent's
  declared task get a relevance boost.
- **Context scoring**: tools matching the session's language, framework, or
  runner profile score higher.
- **Static priors**: initial alpha/beta derived from catalog metadata
  (risk_level, cost_hint, side_effects).
- Returns top-k recommendations with scores.

### 2. Offline Learning Pipeline (`services/tool-learning/`)

A separate microservice runs as a Kubernetes CronJob (hourly + daily):

1. **Read**: Query telemetry Parquet lake (MinIO/S3) via DuckDB — aggregate
   success/failure counts, P95 latency, cost per tool.
2. **Compute**: For each tool, update Beta(alpha, beta) posterior. Apply
   hard constraints (max P95 latency, max error rate, max cost).
3. **Write**: Persist computed policies to Valkey with TTL.
4. **Notify**: Publish `tool.policy.updated` event via NATS so runtime
   instances refresh their recommendation cache.

### Policy Format (Valkey)

```
Key:    tool_policy:<tool_name>
Value:  {
  "tool": "fs.write_file",
  "alpha": 142.0,
  "beta": 8.0,
  "p95_latency_ms": 45,
  "error_rate": 0.053,
  "sample_count": 150,
  "hard_blocked": false,
  "computed_at": "2026-03-30T00:00:00Z"
}
TTL:    2h (configurable)
```

## Consequences

**Positive:**
- Thompson Sampling naturally balances explore/exploit. New tools get sampled
  proportionally to uncertainty. Proven tools dominate as evidence accumulates.
- Hard constraints (policy engine) override statistical recommendations — a
  tool with high success rate but `requires_approval: true` still requires
  approval.
- Offline computation avoids hot-path latency. The workspace service reads
  pre-computed policies from Valkey (sub-ms reads).
- DuckDB enables SQL-based aggregation over Parquet without a persistent
  database. The CronJob runs, computes, writes, and exits — no long-running
  process.

**Negative:**
- Cold start: before the first CronJob run, recommendations rely on the
  heuristic scorer only. Acceptable: the heuristic provides reasonable
  results from catalog metadata alone.
- Feedback loop delay: CronJob runs hourly. A tool that starts failing will
  continue being recommended for up to 1 hour. Mitigated by the policy
  engine's independent authorization (a failing tool produces errors that the
  agent observes directly).
- DuckDB requires CGO_ENABLED=1 for the tool-learning binary, preventing
  static compilation. Mitigated by using `distroless/cc-debian12` (includes
  C runtime) instead of `distroless/static`.

## Update: 2026-04-02 — P0 through P4 Implemented

The original design has been fully implemented and extended:

### What changed

1. **Online policy consumption is active** (P2). The runtime reads learned
   policies from Valkey on every `RecommendTools` call via `ValkeyPolicyReader`.
   This was originally described as "offline only" — it is now a closed loop.

2. **Thompson Sampling runs online** (P3). When policy data has n >= 50 samples,
   the runtime draws from Beta(alpha, beta) posteriors for explore/exploit.
   This replaces the original heuristic-only scorer.

3. **Neural Thompson Sampling added** (P4). A 2-layer MLP (17-dim input, 32
   hidden, 1 output) with last-layer weight perturbation. Activates when
   n >= 100 and a trained model exists in Valkey. Captures non-linear feature
   interactions that the linear Thompson scorer misses.

4. **Algorithm selection is automatic** (P3). `SelectScorerWithModel()` picks:
   NeuralTS > Thompson > Heuristic Policy > Pure Heuristic.

5. **Every recommendation is auditable** (P0). Each call emits a NATS event,
   persists a `RecommendationDecision`, and returns bridge fields (`algorithm_id`,
   `decision_source`, `policy_mode`, `recommendation_id`, `event_id`).

6. **Tool-learning emits lifecycle events** (P1). The pipeline now emits
   `run.started`, `run.completed`, `run.failed`, `policy.computed`, and
   `snapshot.published` — making offline learning auditable.

7. **Contextual bandits implemented** (originally "rejected for now"). LinUCB
   was replaced by HyLinUCB (hybrid linear UCB) in tool-learning, and NeuralTS
   in the runtime. The "feature engineering per invocation context" concern was
   resolved via `context_signature` (task_family:language:constraints_class).

### What did NOT change

- Beta-Binomial posteriors remain the core statistical model.
- Hard constraints (policy engine) still override statistical recommendations.
- DuckDB + Parquet lake architecture for offline aggregation is unchanged.
- CronJob-based offline computation is unchanged.
- Cold start behavior is unchanged (heuristic scorer covers it).

See [Algorithm Architecture](ARCHITECTURE_ALGORITHMS.md) for the full
current implementation.

## Alternatives Considered

1. **UCB1 (Upper Confidence Bound)**: Rejected. UCB1 is deterministic and
   does not model uncertainty distributions. Thompson Sampling provides
   natural exploration through posterior sampling.

2. **Contextual bandits (LinUCB)**: Rejected for now. Would require feature
   engineering per invocation context. The current Beta-Binomial model
   captures tool-level effectiveness without per-context complexity. Could
   be a future enhancement.

3. **LLM-based ranking** (ask the agent to rank tools): Rejected. Consumes
   agent tokens for a meta-decision. The recommendation should be transparent
   to the agent, not require additional reasoning.

4. **Static configuration** (manually ranked tools): Rejected. Does not adapt
   to changing tool effectiveness. Manual maintenance for 96+ tools is
   unsustainable.
