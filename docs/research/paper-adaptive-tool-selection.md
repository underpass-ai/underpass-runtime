# Adaptive Tool Selection for Governed Agent Execution Environments

**Working Draft** — v0.1, 2026-03-31

---

## Thesis

Context-aware hierarchical bandit algorithms with non-stationary adaptation
significantly outperform context-free Thompson Sampling for tool recommendation
in governed AI agent execution environments, as measured by task completion
rate, tool call efficiency, and adaptation speed to changing tool effectiveness.

---

## Research Questions

**RQ1**: Does incorporating task context (language, framework, project type)
into tool selection improve recommendation quality compared to context-free
Thompson Sampling?

**RQ2**: Does exploiting the hierarchical structure of tool families (23 families,
123 tools) accelerate convergence compared to flat arm selection?

**RQ3**: How does non-stationary adaptation (sliding window / discounting)
affect recommendation quality when tool effectiveness changes (new versions,
environment drift)?

**RQ4**: Does LLM-initialized prior knowledge eliminate cold-start degradation
compared to uniform priors?

**RQ5**: Does step-grained reward (per-tool-call feedback) improve learning
speed compared to binary task-level reward?

---

## Proposed Contributions

1. **Context-Aware Tool Selection Model** — A hybrid contextual bandit that
   combines shared parameters (cross-tool patterns) with arm-specific parameters
   (tool metadata: risk, cost, side_effects) for governed tool recommendation.

2. **Hierarchical Family Selection** — A two-level bandit that first selects
   tool families, then tools within families, exploiting the 23-family structure
   for O(log K) convergence improvement.

3. **Non-Stationary Adaptation** — Sliding-window Thompson Sampling (Beta-SWTS)
   with optional discounting for temporal adaptation, benchmarked against
   stationary baselines on real tool telemetry data.

4. **LLM Prior Initialization** — A method for generating informative Beta
   priors from tool catalog metadata + task descriptions via LLM, reducing
   cold-start regret.

5. **Step-Grained Telemetry Reward** — Extension of the telemetry pipeline
   to provide per-invocation quality signals (InvocationQualityMetrics domain
   value object) as multi-dimensional reward for the learning loop.

---

## Experimental Design

### Environment

All experiments run on Underpass Runtime — a governed execution plane with:
- 123 tools across 23 families
- Policy engine (RBAC, risk gating, approval workflows)
- Thompson Sampling pipeline (DuckDB over Parquet, Valkey policies)
- Full telemetry (invocation status, duration, output size, artifacts)
- OpenAPI 3.1 contract for reproducibility

### Baselines

| ID | Algorithm | Context | Priors | Stationarity | Reward |
|---|---|---|---|---|---|
| **B0** | Thompson Sampling (current v1) | None | Uniform Beta(1,1) | Stationary | Binary |
| **B1** | UCB1 | None | N/A | Stationary | Binary |
| **B2** | Epsilon-Greedy (eps=0.1) | None | N/A | Stationary | Binary |

### Experimental Arms

| ID | Algorithm | Context | Priors | Stationarity | Reward |
|---|---|---|---|---|---|
| **E1** | Beta-SWTS (window=200) | None | Uniform | Non-stationary | Binary |
| **E2** | Beta-SWTS + LLM priors | None | LLM-generated | Non-stationary | Binary |
| **E3** | HyLinUCB | Task features | Uniform | Stationary | Binary |
| **E4** | HyLinUCB + family hierarchy | Task + family | Uniform | Stationary | Binary |
| **E5** | HyLinUCB + SWTS + LLM priors | Task features | LLM-generated | Non-stationary | Binary |
| **E6** | E5 + step-grained reward | Task features | LLM-generated | Non-stationary | Multi-dim |

### Context Features (for E3-E6)

| Feature | Source | Type |
|---|---|---|
| `repo_language` | ContextDigest.RepoLanguage | Categorical (13 languages) |
| `project_type` | ContextDigest.ProjectType | Categorical (service, cli, library) |
| `frameworks` | ContextDigest.Frameworks | Multi-hot (11 frameworks) |
| `has_dockerfile` | ContextDigest.HasDockerfile | Binary |
| `has_k8s` | ContextDigest.HasK8sManifests | Binary |
| `test_status` | ContextDigest.TestStatus | Categorical (passing, failing, unknown) |
| `security_posture` | ContextDigest.SecurityPosture | Categorical (clean, warnings) |
| `task_hint_embedding` | Task hint text | Dense vector (embed via LLM) |

### Arm Features (for E3-E6)

| Feature | Source | Type |
|---|---|---|
| `risk_level` | Capability.RiskLevel | Ordinal (low=0, medium=1, high=2) |
| `side_effects` | Capability.SideEffects | Ordinal (none=0, reversible=1, irreversible=2) |
| `requires_approval` | Capability.RequiresApproval | Binary |
| `cost_hint` | Capability.CostHint | Ordinal (free=0, low=1, medium=2, high=3) |
| `family` | Tool name prefix | Categorical (23 families) |
| `timeout_seconds` | Capability.Constraints.TimeoutSeconds | Continuous |

### Metrics

#### Primary Metrics

| Metric | Definition | Measures |
|---|---|---|
| **Task completion rate** | Tasks completed / tasks attempted | Overall effectiveness |
| **Cumulative regret** | Sum of (optimal_reward - actual_reward) over T rounds | Learning efficiency |
| **Tool call efficiency** | Useful tool calls / total tool calls | Recommendation precision |

#### Secondary Metrics

| Metric | Definition | Measures |
|---|---|---|
| **Cold-start regret** | Cumulative regret in first N=50 tasks | Prior quality |
| **Adaptation speed** | Rounds to recover 90% optimal after distribution shift | Non-stationarity response |
| **Family selection accuracy** | Correct family selected / total selections | Hierarchy value |
| **Recommendation acceptance rate** | Recommended tool invoked / recommendations made | Agent trust |

### Evaluation Protocol

1. **Synthetic workload**: Generate task sequences with known optimal tools.
   Vary language, framework, and project type. Inject distribution shifts
   (tool version changes) at known intervals.

2. **Replay evaluation**: Replay historical telemetry from production Parquet
   lake through each algorithm. Measure cumulative regret against hindsight
   optimal.

3. **Live A/B test**: Deploy E5 alongside B0 in parallel sessions. Compare
   task completion rate and tool call efficiency over 1000 sessions.

### Statistical Analysis

- Wilson score 95% confidence intervals for all rate metrics.
- Bootstrap 95% CI for cumulative regret comparisons.
- Minimum 3 random seeds per synthetic experiment.
- Exclusion criteria: sessions with <3 tool calls (insufficient signal).

---

## Data Sources

### Telemetry Pipeline (existing)

```
Invocation completes
  → TelemetryRecord (19 fields)
  → Valkey list (7-day TTL)
  → Aggregator (5-min loop → ToolStats)
  → Parquet lake (S3, Hive-partitioned by dt/hour)
  → DuckDB aggregation (SELECT ... GROUP BY context_signature, tool_id)
  → Thompson Sampling (Beta posteriors)
  → ToolPolicy (Valkey, 2h TTL)
  → NATS event (tool.policy.updated)
```

### New Data Required (v2)

| Data | Source | Storage | Purpose |
|---|---|---|---|
| Task context features | ContextDigest (exists, unused) | Extend TelemetryRecord | Context-aware bandits |
| Tool outcome quality | InvocationQualityMetrics (exists) | Extend TelemetryRecord | Multi-dim reward |
| Task-level outcome | Agent feedback (new) | New field in telemetry | Task completion signal |
| Tool chain sequences | Ordered invocations per session | Session-level aggregation | Combinatorial bandits |

---

## Implementation Plan

### Phase 1: Foundation (Weeks 1-2)

- [ ] Populate unused TelemetryRecord fields (RepoLanguage, ProjectType, Approved)
- [ ] Wire ContextDigest into TelemetryRecord construction
- [ ] Wire InvocationQualityMetrics into TelemetryRecord
- [ ] Add task-level outcome field to telemetry
- [ ] Extend Parquet schema with new fields
- [ ] Update DuckDB queries for contextual aggregation

### Phase 2: Algorithms (Weeks 3-4) — COMPLETE

- [x] Implement Beta-SWTS in tool-learning pipeline (PR #66, merged)
- [x] Implement LLM prior initialization (PR #69)
- [x] Implement HyLinUCB with hybrid payoff (PR #68)
- [ ] Implement hierarchical family selector
- [ ] Add step-grained reward computation

### Phase 3: Evaluation (Weeks 5-6)

- [ ] Build synthetic workload generator
- [ ] Build replay evaluation harness
- [ ] Run B0, B1, B2 baselines
- [ ] Run E1-E6 experimental arms
- [ ] Statistical analysis and paper results

### Phase 4: Paper (Weeks 7-8)

- [ ] Write methodology section
- [ ] Write results section with evidence tables
- [ ] Write discussion (what worked, what didn't, ablation)
- [ ] Submission draft

---

## Code References

| Component | Path | Status |
|---|---|---|
| Current discovery | `internal/app/discovery.go` | v1 (flat, no context) |
| Current recommender | `internal/app/recommender.go` | v1 (static scoring + telemetry boost) |
| Current TS pipeline | `services/tool-learning/internal/domain/learning.go` | v1 (Beta-Binomial) |
| Context digest | `internal/app/context_digest.go` | Built, not wired to recommendations |
| Quality metrics | `internal/domain/quality_metrics.go` | Built, not wired to telemetry record |
| Telemetry record | `internal/app/telemetry_types.go` | 5 fields unused |
| DuckDB aggregation | `services/tool-learning/internal/adapters/duckdb/lake_reader.go` | Context-free aggregation |
| Tool policy | `services/tool-learning/internal/domain/tool_policy.go` | No context signature from runtime |

---

## Related Work

See [sota-tool-selection.md](sota-tool-selection.md) for full literature survey.

Key references:
- Neural-LinUCB (Xu et al., NeurIPS 2021) — deep context + shallow exploration
- HyLinUCB (Das & Sinha, ECML PKDD 2024) — hybrid shared + arm-specific
- Beta-SWTS (Fiandri et al., JAIR 2024) — sliding-window non-stationary TS
- Jump-Starting Bandits (Alamdari et al., EMNLP 2024) — LLM prior initialization
- ToolRL (Qian et al., NeurIPS 2025) — multi-dimensional reward design
- StepTool (Yu et al., CIKM 2025) — step-grained RL for tool learning
- OTC (Wang et al., 2025) — optimal tool calls via efficiency-aware RL
