# Changelog

## 2026-04-03

### Gap Sweep — 24 PRs (#88-#105)

**Learning Evidence Plane**
- Durable Valkey backend for RecommendationDecisionStore (30-day TTL) (#88)
- 6 new LearningEvidenceService RPCs: GetLearningStatus, GetPolicy, ListPolicies, GetAggregate (#90)
- Telemetry TTL enforcement (Expire after RPush) + bounded LRange (10k cap) (#90)
- Subscribe to all 6 tool_learning.* NATS subjects (#90)
- Context signature computed for denied invocations (#91)
- Proto enum DECISION_SOURCE_NEURAL_TS + PolicyMode constants (#97)

**NeuralTS End-to-End**
- Wire TrainNeuralModel in tool-learning CronJob → publishes model to Valkey (#92)
- Telemetry-to-Lake exporter (Valkey → Parquet/S3) + E2E test (#95)

**Agent Feedback Loop**
- AcceptRecommendation / RejectRecommendation gRPC RPCs (#99)
- Domain events: recommendation.accepted / recommendation.rejected
- E2E validated on cluster

**Explainability Trace**
- Structured score_breakdown per recommendation (heuristic → telemetry → policy) (#100)
- Persisted in decision store, exposed in both runtime and learning protos

**Cross-Agent Learning**
- CrossAgentInsight in every recommendation response (#103)
- Confidence levels: low/medium/high based on collective invocation count

**Autonomous Remediation**
- remediation-agent: NATS alert subscriber → playbook execution → feedback (#101)
- 4 built-in playbooks (failure rate, latency, denials, health)

**Workspace Prewarming**
- Background pre-load of policies, stats, model at session creation (#102)
- Zero cold-start on first RecommendTools call

**Observability**
- TraceLogHandler: trace_id/span_id injected into every structured log (#93)
- Metrics port 9090 exposed in Deployment + Service (#93)
- Observability stack: [underpass-ai/underpass-observability](https://github.com/underpass-ai/underpass-observability)
  (Grafana, Loki, OTEL Collector, Prometheus, alert-relay)
- Grafana dashboard (8 panels) auto-provisioned
- Route53 DNS: grafana.underpassai.com

**Quality**
- 12x cleanup error logging (replaced _ = with slog.Warn) (#89)
- 5 KPI metrics wired into hot paths (#96)
- README algorithm claims corrected (online vs offline) (#98)
- Comprehensive documentation update (#104)

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
