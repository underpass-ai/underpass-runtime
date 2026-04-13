# State of the Art: Learning Algorithms for Tool Selection in Agent Systems

Literature survey for the adaptive tool selection research program.
Last updated: 2026-03-31.

---

## Problem Statement

An AI agent runtime with 123 tools across 23 families must recommend the best
tools for a given task. The current system (v1) uses Thompson Sampling with
Beta-Binomial posteriors tracking simple success/failure per tool. This survey
covers approaches for a more robust v2 that considers context, tool metadata,
sequences, non-stationarity, and cost.

## Current System (v1 Baseline)

- **Algorithm**: Thompson Sampling, Beta(alpha, beta) per tool
- **Context**: None — each tool scored independently of task
- **Priors**: Uniform Beta(1,1)
- **Reward**: Binary (success/failure)
- **Update**: Offline CronJob (hourly/daily) via DuckDB over Parquet lake
- **Limitations**: No context, no tool metadata, no sequencing, no temporal adaptation

---

## 1. Contextual Bandits

### 1.1 Neural-LinUCB — Deep Representation + Shallow Exploration

**Paper**: Xu, Wen, Zhao, Gu. "Neural Contextual Bandits with Deep Representation
and Shallow Exploration." NeurIPS 2021. [arxiv:2012.01780](https://arxiv.org/abs/2012.01780)

Deep ReLU network learns nonlinear context representations; LinUCB explores
only in the final linear layer. Decouples representation learning from
exploration.

**Applicability**: Encode task context (language, framework, project type) through
deep layers. UCB exploration over 123 tools in learned feature space. Immediately
recommends tools for task types seen before, even for new tool-task pairs.

**vs. v1**: Context-conditional. Regret O(sqrt(T)) vs linear for context-free TS
on structured problems.

**Complexity**: Medium-high. Requires neural network training.

### 1.2 HyLinUCB — Hybrid Payoff with Shared + Arm-Specific Parameters

**Paper**: Das, Sinha. "Linear Contextual Bandits with Hybrid Payoff: Revisited."
ECML PKDD 2024. [arxiv:2406.10131](https://arxiv.org/abs/2406.10131)

Shared parameters capture global patterns ("Python projects benefit from linting
tools"), arm-specific parameters capture tool-unique effects. Regret scales
sub-linearly with arm count.

**Applicability**: Direct fit. Shared parameters = cross-tool patterns. Arm
features = risk_level, cost, side_effects, family. Tools within a family share
statistical strength.

**vs. v1**: Shares learning across tools. TS learns each independently.

**Complexity**: Medium. Standard linear algebra, no neural networks.

### 1.3 LLM-Initialized Priors — Jump-Starting Bandits

**Paper**: Alamdari, Cao, Wilson. "Jump Starting Bandits with LLM-Generated
Prior Knowledge." EMNLP 2024. [aclanthology:2024.emnlp-main.1107](https://aclanthology.org/2024.emnlp-main.1107/)

LLMs generate synthetic preference data to initialize bandit priors.
Eliminates cold-start.

**Applicability**: Prompt LLM with tool catalog + task description → get
initial Beta(alpha, beta) priors instead of uniform Beta(1,1). The LLM
"knows" git.clone is relevant for repo setup without seeing data.

**vs. v1**: Eliminates cold-start. Current uniform priors waste exploration
on obviously wrong tools.

**Complexity**: Low. Initialization strategy only — keep TS, replace priors.

### 1.4 Variance-Aware Feel-Good Thompson Sampling

**Paper**: Li et al. "Variance-Aware Feel-Good Thompson Sampling for Contextual
Bandits." 2024-2025. [arxiv:2511.02123](https://arxiv.org/abs/2511.02123)

Adds variance-related weights and optimistic exploration terms to TS posterior.
Near-optimal regret for general contextual bandits.

**Applicability**: Direct upgrade. Context-dependent variance weighting:
explore uncertain tool-context pairs more, exploit known ones.

**vs. v1**: Tighter regret bounds. Adapts exploration per tool-context pair.

**Complexity**: Medium. Modifies sampling distribution.

---

## 2. Hierarchical and Structured Arms

### 2.1 Top-k Extreme Contextual Bandits with Arm Hierarchy

**Paper**: Sen et al. "Top-k eXtreme Contextual Bandits with Arm Hierarchy."
ICML 2021. [arxiv:2102.07800](https://arxiv.org/abs/2102.07800)

Exploits hierarchical structure among arms for exponential search space
reduction. Tested with 3M arms, inference in ~8ms.

**Applicability**: Our 23 families ARE a hierarchy. First select families
(git, fs, k8s), then tools within families. Reduces effective search from
99 to ~23 + ~4-5 per family. Top-k naturally recommends tool chains.

**vs. v1**: Exploits family structure. Regret O(k*sqrt(log A * T)) with
hierarchy vs O(k*sqrt(A*T)) without.

**Complexity**: Medium. Requires two-level model.

---

## 3. Combinatorial Bandits (Tool Chains)

### 3.1 Contextual Combinatorial Cascading Bandits (C3-UCB)

**Paper**: Li et al. "Contextual Combinatorial Cascading Bandits." ICML 2016
+ extensions 2024. [arxiv:2508.13981](https://arxiv.org/abs/2508.13981)

Agent selects an ordered list of tools; execution proceeds until stopping
criterion. Context guides selection. O(sqrt(T)) regret.

**Applicability**: When agents try tools sequentially (try grep first, then
ast_parse, then llm_query), this models the optimal ordering per context.

**vs. v1**: Models tool ordering and early-stopping. Reduces wasted calls.

**Complexity**: Medium. Ordered list optimization.

### 3.2 Combinatorial Logistic Bandits (VA-CLogUCB)

**Paper**: Liu et al. "Combinatorial Logistic Bandits." SIGMETRICS 2025.
[arxiv:2410.17075](https://arxiv.org/abs/2410.17075)

Binary outcomes per base arm, logistic model for chain-level success.
Variance-adaptive UCB.

**Applicability**: Tool chain success depends on tool ordering and compatibility.
Joint optimization of chain selection with O(d*sqrt(T)) regret.

**vs. v1**: TS on individual tools cannot model chain-level success.

**Complexity**: High. Combinatorial optimization per step.

---

## 4. Reinforcement Learning for Tool Use

### 4.1 ToolRL — Reward is All Tool Learning Needs

**Paper**: Qian et al. "ToolRL: Reward is All Tool Learning Needs." NeurIPS 2025.
[arxiv:2504.13958](https://arxiv.org/abs/2504.13958)

First comprehensive study on reward design for tool selection via RL. GRPO
with fine-grained reward signals. 17% improvement over base models.

**Applicability**: Multi-dimensional rewards (invocation correctness, parameter
accuracy, task progress, efficiency) instead of binary success/failure.

**vs. v1**: Multi-dimensional reward captures partial success. TS binary
rewards lose information.

**Complexity**: High. Requires RL training infrastructure.

### 4.2 StepTool — Step-Grained RL

**Paper**: Yu et al. "StepTool: A Step-grained Reinforcement Learning Framework
for Tool Learning in LLMs." CIKM 2025. [arxiv:2410.07745](https://arxiv.org/abs/2410.07745)

Per-tool-call rewards based on success AND contribution to task.

**Applicability**: Per-step feedback directly available in our runtime.
~10x more training signal from same number of tasks.

**vs. v1**: Current TS updates only on task completion. Step-grained
accelerates learning dramatically.

**Complexity**: Medium-high.

### 4.3 OTC — Optimal Tool Calls via RL

**Paper**: Wang et al. "OTC: Optimal Tool Calls via Reinforcement Learning." 2025.
[arxiv:2504.14870](https://arxiv.org/abs/2504.14870)

Jointly optimizes accuracy AND minimal tool calls. Reduces tool calls by
68.3% while maintaining accuracy. Tool productivity +215%.

**Applicability**: Critical for cost-sensitive environments. Recommends
minimum effective tool set.

**vs. v1**: TS maximizes per-tool success but doesn't penalize excessive
tool use.

**Complexity**: Medium. Standard PPO/GRPO with modified reward.

---

## 5. Non-Stationary Bandits

### 5.1 Sliding-Window Thompson Sampling (Beta-SWTS)

**Paper**: Fiandri, Metelli, Trovo. "Sliding-Window Thompson Sampling for
Non-Stationary Settings." JAIR 2024. [arxiv:2409.05181](https://arxiv.org/abs/2409.05181)

Sliding window over recent observations for TS updates. Beta-SWTS uses
Beta priors for Bernoulli rewards — directly applicable to success/failure.

**Applicability**: MINIMAL change from v1. Instead of Beta(alpha_total,
beta_total), use Beta(alpha_recent_N, beta_recent_N). Naturally adapts
when tool effectiveness changes.

**vs. v1**: Handles non-stationarity. Current TS weights all history equally.

**Complexity**: Very low. Add window parameter, track recent N observations.

### 5.2 Discounted Thompson Sampling (DS-TS)

**Paper**: Qi et al. "Discounted Thompson Sampling for Non-Stationary Bandit
Problems." 2023. [arxiv:2305.10718](https://arxiv.org/abs/2305.10718)

Discount factor down-weights old observations. Handles both abrupt changes
(tool version updates) and smooth drift (environment changes).

**Applicability**: Direct upgrade. When tool is updated or deprecated,
DS-TS naturally down-weights old data.

**vs. v1**: Current TS never forgets. A tool deprecated 6 months ago
still shows historical success rates.

**Complexity**: Low. Replace update rule with discounted version.

---

## 6. Dueling and Preference-Based

### 6.1 LLM-Enhanced Multi-Armed Bandits

**Paper**: Sun et al. "Large Language Model-Enhanced Multi-Armed Bandits." 2025.
[arxiv:2502.01118](https://arxiv.org/abs/2502.01118)

LLMs predict rewards within classical MAB. TS with LLM-based reward prediction
and decaying temperature. Extends to dueling bandits.

**Applicability**: LLM predicts "how useful will tool X be for task Y?" as
reward estimate. Captures semantic tool-task relationships instantly (zero-shot).

**vs. v1**: TS needs hundreds of trials. LLM reward prediction is immediate.

**Complexity**: Medium. LLM inference for reward prediction.

---

## 7. Learning to Rank

### 7.1 Listwise Preference Optimization (LiPO)

**Paper**: NAACL 2025. [aclanthology:2025.naacl-long.121](https://aclanthology.org/2025.naacl-long.121.pdf)

Rank candidates by contribution to task success using ranking loss.
Works with preference feedback (relative, not absolute).

**Applicability**: After each task, rank tools by contribution. No absolute
success needed — only "tool A was more useful than tool B."

**vs. v1**: Works with richer feedback signals.

**Complexity**: Medium.

---

## 8. Key Surveys

- Bouneffouf & Feraud. "Survey: Multi-Armed Bandits Meet Large Language Models." 2025. [arxiv:2505.13355](https://arxiv.org/abs/2505.13355)
- Qin et al. "ToolLLM." ICLR 2024 Spotlight. [arxiv:2307.16789](https://arxiv.org/abs/2307.16789)
- Patil et al. "Gorilla." NeurIPS 2024. [arxiv:2305.15334](https://arxiv.org/abs/2305.15334)

---

## Proposed Implementation Roadmap

### Phase 1 — Low effort, high impact
1. **Sliding-Window TS (Beta-SWTS)** — window of last N=200 observations. Minimal code change.
2. **LLM-initialized priors** — replace uniform Beta(1,1) with LLM-generated priors.

### Phase 2 — Medium effort
3. **HyLinUCB with hybrid payoff** — add context features + arm features.
4. **Hierarchical selection** — exploit 23-family structure.
5. **Discounted TS** — temporal adaptation alongside sliding window.

### Phase 3 — High effort, highest ceiling
6. **Neural-LinUCB** — deep representation + shallow exploration.
7. **Step-grained rewards** — per-tool-call reward signals.
8. **Cascading bandits** — ordered tool chain recommendation.
