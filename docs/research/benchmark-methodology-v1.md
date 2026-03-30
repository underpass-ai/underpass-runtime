# Benchmark Methodology v1

**Status**: Active — all evaluation runs must reference this document.
**Version**: 1.0 (2026-03-31)

---

## 1. Purpose

Canonical rules for evaluating tool selection algorithms in underpass-runtime.
Every benchmark run must follow this methodology for reproducibility.

## 2. Single-Variable Rule

Each comparison must change exactly one variable. Examples:
- B0 vs E1: same everything, only add sliding window
- E2 vs E3: same priors, only add context features
- Violating this rule invalidates the comparison.

## 3. Algorithms Under Test

| ID | Algorithm | Context | Priors | Window | Reward |
|---|---|---|---|---|---|
| B0 | Thompson Sampling (v1) | None | Uniform | None | Binary |
| B1 | UCB1 | None | N/A | None | Binary |
| B2 | Epsilon-Greedy (0.1) | None | N/A | None | Binary |
| E1 | Beta-SWTS | None | Uniform | 200 | Binary |
| E2 | Beta-SWTS + LLM priors | None | LLM | 200 | Binary |
| E3 | HyLinUCB | Task+Arm | Uniform | None | Binary |
| E4 | HyLinUCB + hierarchy | Task+Arm+Family | Uniform | None | Binary |
| E5 | HyLinUCB + SWTS + LLM | Task+Arm | LLM | 200 | Binary |
| E6 | E5 + step reward | Task+Arm | LLM | 200 | Multi-dim |

## 4. Workload Generation

### Synthetic Workloads

Generate task sequences with `cmd/benchmark-gen/` (to be built):

| Parameter | Values |
|---|---|
| Languages | go, python, javascript, rust, java |
| Project types | service, cli, library |
| Task categories | build, test, deploy, debug, refactor, security |
| Tools per task (optimal) | 1-5 |
| Distribution shifts | At rounds 200, 500, 800 |
| Total rounds | 1000 |
| Random seeds | 3 minimum (42, 137, 256) |

### Replay Workloads

Replay production telemetry from Parquet lake:
- Source: `s3://telemetry-lake/dt=*/hour=*/*.parquet`
- Filter: `ts >= '2026-01-01' AND ts < '2026-04-01'`
- Replay preserves original ordering within sessions

## 5. Metrics

### Primary (must report)

| Metric | Definition | Aggregation |
|---|---|---|
| Cumulative regret | Sum(optimal_reward - actual_reward) over T | Total |
| Task completion rate | Completed tasks / attempted tasks | Mean + 95% CI |
| Tool call efficiency | Useful calls / total calls | Mean + 95% CI |

### Secondary (should report)

| Metric | Definition | Aggregation |
|---|---|---|
| Cold-start regret (R50) | Cumulative regret at T=50 | Total |
| Adaptation speed | Rounds to 90% optimal after shift | Mean + 95% CI |
| Family accuracy | Correct family / total selections | Mean |
| Acceptance rate | Recommended used / recommended total | Mean |
| P95 recommendation latency | 95th percentile of scoring time | ms |

## 6. Execution

### Checklist (before every run)

- [ ] Verify algorithm ID matches single-variable rule
- [ ] Verify workload parameters match this document
- [ ] Record git SHA of underpass-runtime
- [ ] Record git SHA of tool-learning service
- [ ] Record random seed(s)
- [ ] Verify telemetry pipeline is recording

### Run Command (synthetic)

```bash
go run ./cmd/benchmark-gen \
  --algorithm=E1 \
  --seed=42 \
  --rounds=1000 \
  --shifts=200,500,800 \
  --output=artifacts/benchmark-runs/$(date +%Y-%m-%d_%H%M%S)/
```

### Run Command (replay)

```bash
go run ./cmd/benchmark-replay \
  --algorithm=E1 \
  --parquet=s3://telemetry-lake/ \
  --from=2026-01-01 \
  --to=2026-04-01 \
  --output=artifacts/benchmark-runs/$(date +%Y-%m-%d_%H%M%S)/
```

## 7. Artifact Structure

```
artifacts/benchmark-runs/YYYY-MM-DD_HHMMSS/
├── config.json          (algorithm ID, parameters, seed, git SHA)
├── run.log              (full execution log)
├── summary.json         (all metrics)
├── regret_curve.json    (cumulative regret per round)
├── report.md            (markdown summary with tables)
└── results/
    ├── round_0001.json  (per-round: task, selected tool, reward, regret)
    └── ...
```

## 8. Reproducibility Requirements

Every published result must include:
- Algorithm ID from this document
- Git SHA of underpass-runtime at time of run
- Git SHA of tool-learning service
- Random seed(s) used
- Workload parameters (or Parquet date range)
- config.json from artifact directory

## 9. Statistical Analysis

- **Confidence intervals**: Wilson score 95% CI for all rate metrics
- **Regret comparison**: Bootstrap 95% CI on cumulative regret difference
- **Significance**: Paired comparison across seeds (Wilcoxon signed-rank)
- **Seeds**: Minimum 3, recommended 5
- **Effect size**: Report Cohen's d for primary metrics

## 10. Exclusion Criteria

Exclude sessions/rounds from analysis if:
- Fewer than 3 tool calls in the task
- Infrastructure failure (tool timeout, network error)
- Policy denial rate > 50% (misconfigured session)

Document all exclusions with counts and reasons.

## 11. Version History

| Version | Date | Changes |
|---|---|---|
| 1.0 | 2026-03-31 | Initial methodology |
