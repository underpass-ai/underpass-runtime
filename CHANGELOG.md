# Changelog

## 2026-04-02

### P0: Recommendation Evidence (#80)

- `RecommendTools` returns bridge fields: `recommendation_id`, `event_id`,
  `event_subject`, `decision_source`, `algorithm_id`, `algorithm_version`,
  `policy_mode`
- Emits `runtime.learning.recommendation.emitted` to NATS JetStream
- Persists `RecommendationDecision` per call
- `GetRecommendationDecision` and `GetEvidenceBundle` gRPC endpoints
- E2E test infrastructure refactored: unified Docker image, Helm-native
  `helm test`, JetStream subscribers, fail-fast (no fallbacks)

### P1: Auditable Run Lifecycle (#81)

- Tool-learning pipeline emits: `run.started`, `run.completed`, `run.failed`,
  `policy.computed`, `snapshot.published`
- `PolicyRun` domain entity with lifecycle tracking
- Every run gets a UUID `run_id` for correlation across events

### P2: Online Policy Consumption (#83)

- `ValkeyPolicyReader` wired in runtime — reads learned policies on every
  recommendation call
- `context_signature` always computed (not gated by policy availability)
- `policy_notes` populated with source info when learned policy applied
- NATS subscriber for `tool_learning.policy.updated` (observable loop proof)
- CI automation: all images build+push on merge to main

### P3: Algorithm Selection (#84)

- `RecommendationScorer` interface: pluggable scoring strategies
- `ThompsonScorer`: Beta-distribution sampling when n >= 50
- `HeuristicPolicyScorer`: fixed-weight fallback for n < 50
- `SelectScorer`: automatic algorithm selection based on policy maturity
- `algorithm_id` and `decision_source` reflect the actual scorer used

### P4: Neural Thompson Sampling (#85)

- Pure Go MLP (17 input, 32 hidden, 1 output) with Xavier initialization
- `NeuralTSScorer`: forward pass with last-layer weight perturbation
  (sigma = 1/sqrt(n)) for exploration
- `NeuralTrainer` in tool-learning: mini-batch SGD with binary cross-entropy
- Model weights stored in Valkey as JSON, loaded on each recommendation
- Algorithm selection: NeuralTS > Thompson > Heuristic (automatic)

### Infrastructure

- SonarCloud security hotspots resolved (#82)
- E2E test 18: policy-driven recommendations (seeds Valkey, verifies
  `decision_source=heuristic_with_learned_policy`)
- E2E test 19: NeuralTS recommendations (seeds MLP model + policies,
  verifies `algorithm_id=neural_thompson_sampling`)
- `.dockerignore` updated for E2E image builds
- `cmd/` excluded from SonarCloud coverage (wiring code)
