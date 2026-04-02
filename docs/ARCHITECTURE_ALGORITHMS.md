# Algorithm Architecture

## Overview

The recommendation engine uses a **4-tier scoring stack** that automatically
selects the most appropriate algorithm based on available data maturity.

```
Request: RecommendTools(session_id, task_hint, top_k)
    |
    v
1. Heuristic base score (always)
    |-- risk penalty, cost penalty, side effects, approval
    |-- task hint token matching (+0.20 max bonus)
    |
2. Telemetry boost (when stats available, n >= 5)
    |-- success rate bonus (+0.15)
    |-- p95 latency penalty (-0.10)
    |-- deny rate penalty (-0.10)
    |
3. Learned policy scoring (when policies in Valkey)
    |-- Algorithm selected by SelectScorerWithModel():
    |   |
    |   |-- n >= 100 + neural model:  NeuralTS
    |   |-- n >= 50:                  Thompson Sampling
    |   |-- n < 50:                   Heuristic Policy Scorer
    |
4. Evidence emission
    |-- RecommendationDecision persisted
    |-- NATS event emitted
    |-- Bridge fields returned in response
```

## Algorithm Selection

The `SelectScorerWithModel()` function picks the scorer automatically:

| Priority | Scorer | Activates when | `algorithm_id` | `decision_source` |
|----------|--------|---------------|-----------------|-------------------|
| 1 | NeuralTS | n >= 100 + trained MLP in Valkey | `neural_thompson_sampling` | `neural_thompson_sampling` |
| 2 | Thompson | n >= 50 | `beta_thompson_sampling` | `learned_policy_thompson` |
| 3 | Heuristic Policy | n >= 10 | `heuristic_v1` | `heuristic_with_learned_policy` |
| 4 | No scorer | No policies | `heuristic_v1` | `heuristic_only` or `heuristic_with_telemetry` |

`n` = maximum `n_samples` across all policies for the current context signature.

## Heuristic Scorer

Static scoring from the tool catalog metadata. Always runs as the base.

```
base_score = 1.0
- risk_penalty:     low=0, medium=-0.15, high=-0.35
- side_effects:     none=0, reversible=-0.10, irreversible=-0.25
- approval_penalty: -0.10 if requires_approval
- cost_penalty:     cheap=0, medium=-0.05, expensive=-0.15
+ hint_bonus:       +0.20 * min(matched_tokens / total_tokens, 1.0)
```

## Thompson Sampling Scorer

Uses the Beta posterior from offline policy computation. Draws a random
sample from Beta(alpha, beta) and blends it with the heuristic base.

```
sample = Beta(policy.Alpha, policy.Beta)  // random draw
weight = min(n_samples / 100, 0.8)       // trust factor
score  = (1 - weight) * base + weight * sample
```

The randomness provides **natural exploration**: tools with uncertain
posteriors (low n) get more variance, so they get tried more often.
As data accumulates, the variance shrinks and the scorer converges.

SLO penalties are applied on top:
- error_rate > 30%: -0.20
- p95_latency > 15s: -0.10

## Neural Thompson Sampling (NeuralTS)

A 2-layer MLP that captures **non-linear feature interactions** that the
linear Thompson scorer misses.

### Architecture

```
Input (17 features)
    |
    v
[Linear: 17 x 32] + bias(32)
    |
    v
[ReLU]
    |
    v
[Linear: 32 x 1] + bias(1)     <-- perturbed for exploration
    |
    v
[Sigmoid]
    |
    v
Output: reward probability [0, 1]
```

Total parameters: 609 (17*32 + 32 + 32*1 + 1).

### Feature Vector (17 dimensions)

| Index | Feature | Source |
|-------|---------|--------|
| 0 | Confidence | policy.Alpha / (Alpha + Beta) |
| 1 | Error rate | policy.ErrorRate |
| 2 | Latency (log-normalized) | log1p(p95_latency_ms) / 10 |
| 3 | Sample size (log-normalized) | log1p(n_samples) / 10 |
| 4 | Alpha/Beta ratio | policy.Alpha / (Alpha + Beta) |
| 5 | Cost (log-normalized) | log1p(p95_cost) / 5 |
| 6-16 | Reserved | Context features (language, task family, constraints) |

### Exploration via Weight Perturbation

NeuralTS explores by adding Gaussian noise to the **last-layer weights**
during inference:

```
sigma = 1 / sqrt(n_samples)           // exploration magnitude
W2_perturbed = W2 + N(0, sigma^2)     // perturb last layer only
score = forward(features, W1, b1, W2_perturbed, b2)
```

As `n_samples` grows, sigma shrinks, and the scorer converges to the
deterministic prediction. This is the **Neural Thompson Sampling**
mechanism from Zhang et al. (NeurIPS 2021).

### Blending

```
neural_prob = sigmoid(forward_perturbed(features))
weight = min(n_samples / 200, 0.9)
score = (1 - weight) * heuristic_base + weight * neural_prob
```

### Model Storage

The trained model is stored in Valkey at key `neural_ts:model:v1` as JSON:

```json
{
  "w1": [609 floats],
  "b1": [32 floats],
  "w2": [32 floats],
  "b2": [1 float]
}
```

Training is performed offline by the tool-learning service using mini-batch
SGD with binary cross-entropy loss.

## Context Signature

Every recommendation is computed for a **context signature** derived from
the workspace:

```
context_signature = task_family:language:constraints_class
```

- **task_family**: io, vcs, build, deploy, network, data, exec, quality, general
- **language**: go, python, javascript, rust, java, unknown
- **constraints_class**: standard, constraints_high (AllowedPaths set), constraints_low (platform_admin)

Policies are keyed by context signature in Valkey:
`tool_policy:{context_signature}:{tool_id}`

## Evidence and Observability

Every `RecommendTools` call produces:

| Field | Where | Description |
|-------|-------|-------------|
| `recommendation_id` | gRPC response | Stable ID for the decision |
| `event_id` | gRPC response + NATS event | Links response to event |
| `algorithm_id` | gRPC response + decision | Which scorer ran |
| `algorithm_version` | gRPC response + decision | Scorer version |
| `decision_source` | gRPC response + decision | Tier classification |
| `policy_mode` | gRPC response + decision | none, shadow, assist, enforced |
| `context_signature` | Decision | Context grouping key |
| `policy_notes` | Per-recommendation | Algorithm + policy source info |

Query the evidence via gRPC:
- `LearningEvidenceService.GetRecommendationDecision(recommendation_id)`
- `LearningEvidenceService.GetEvidenceBundle(recommendation_id)`

## Tool-Learning Pipeline

The offline pipeline (CronJob) feeds the online scorers:

```
Parquet lake (DuckDB) --> Aggregate stats per (context, tool)
    |
    v
Thompson Sampling: compute Beta(alpha, beta) posteriors
    |
    v
Neural Trainer: train MLP from aggregate features (SGD, cross-entropy)
    |
    v
Write to Valkey:
  - tool_policy:{context}:{tool} = JSON policy
  - neural_ts:model:v1 = JSON MLP weights
    |
    v
Emit events:
  - tool_learning.run.started
  - tool_learning.policy.computed
  - tool_learning.snapshot.published
  - tool_learning.run.completed
  - tool_learning.policy.updated
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `POLICY_KEY_PREFIX` | `tool_policy` | Valkey key prefix for learned policies |
| `INVOCATION_STORE_BACKEND` | `memory` | Set to `valkey` to enable policy reader |

The policy reader activates automatically when `INVOCATION_STORE_BACKEND=valkey`.
The neural model is loaded from Valkey on each recommendation call (no cache — always fresh).
