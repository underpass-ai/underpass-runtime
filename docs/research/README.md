# Research — Underpass Runtime

Research program for advancing tool selection, recommendation, and learning
algorithms in governed agent execution environments.

## Active Research

| Paper | Status | Focus |
|---|---|---|
| [Adaptive Tool Selection](paper-adaptive-tool-selection.md) | Draft — Phase 1 complete | Contextual bandits + hierarchical selection for governed tool recommendation |

## Implemented Algorithms

| Algorithm | PR | Status | Evidence |
|---|---|---|---|
| [Beta-SWTS](../adr/ADR-003-thompson-sampling-tool-recommendations.md) | #66 | Merged | Sliding window detects tool degradation in real-time |
| [HyLinUCB](sota-tool-selection.md#12-hylinucb) | #68 | In review | E2E benchmark: 14 invocations, 2 contexts, scoring differentiated |
| [LLM Priors](sota-tool-selection.md#13-llm-initialized-priors) | #69 | In review | Qwen3-8B generates priors (0.50-0.98) with risk differentiation |

## E2E Evidence

| Test | What it proves | Model | Result |
|---|---|---|---|
| E2E 15: vLLM Learning Loop | Full cycle: vLLM → discovery → invoke → telemetry → recommendations adapt | Qwen3-8B | PASSED — learning signal confirmed |
| E2E 16: HyLinUCB Benchmark | Contextual bandit scoring with arm features across 2 contexts | Qwen3-8B | PASSED — 3 arms learned, scores differentiated |
| E2E: LLM Prior Generation | Cold-start elimination via LLM-estimated success probabilities | Qwen3-8B | PASSED — avg low-risk=0.96, high-risk=0.55 |

## Methodology

| Document | Description |
|---|---|
| [Benchmark Methodology v1](benchmark-methodology-v1.md) | Canonical rules for evaluation runs |
| [State of the Art](sota-tool-selection.md) | Literature survey: 20+ papers (2020-2025) |

## Research Roadmap

### Phase 1: Foundation (COMPLETE)

- [x] Beta-SWTS sliding-window Thompson Sampling (PR #66)
- [x] LLM-initialized priors for cold-start elimination (PR #69)
- [x] HyLinUCB contextual bandit with hybrid payoff (PR #68)
- [x] E2E 15: vLLM-driven learning loop (PR #67)
- [x] E2E 16: HyLinUCB benchmark binary
- [x] E2E: LLM prior generation validation

### Phase 2: Pipeline Integration (NEXT)

- [ ] Wire HyLinUCB into ComputePolicyUseCase as alternative scorer
- [ ] Wire LLM priors into tool-learning CronJob initialization
- [ ] Integrate ContextDigest into TelemetryRecord
- [ ] Add `--algorithm` flag to select TS vs HyLinUCB vs TS+LLM-priors
- [ ] Hierarchical family selector (exploit 23-family structure)

### Phase 3: Evaluation

- [ ] Build synthetic workload generator
- [ ] Build replay evaluation harness
- [ ] Run baselines (B0-B2) vs experimental arms (E1-E6)
- [ ] Statistical analysis with Wilson 95% CI
- [ ] Write paper results section

## Incidents

Post-mortems and learnings from benchmark runs tracked in `incidents/`.
