# Plan — PIR-side integration once runtime tools land

Status: planned, gated on the two runtime-side proposals
Source repo: `underpass-payments-incident-response`
Owner: PIR
Date: 2026-04-26

## Summary

This is the PIR-side execution plan for the work item tracked in
`docs/implementation-status.md` §"Where to pick up next" #1
("Runtime registration of saturation + notification +
payment-integrity tools"). Two cross-repo proposals already specify
the runtime work:

- `docs/proposals/runtime-saturation-and-notify-tools-registration.md`
  — three K8s saturation tools + one notify tool.
- `docs/proposals/runtime-payment-integrity-tools-registration.md`
  — two FINANCIAL_IMPACT-class payment tools.

Both are status `proposed, awaiting runtime team review`. Once each
runtime tool lands in `underpass-runtime`'s `DefaultCapabilities()`,
the PIR side gains a follow-up slice that turns its v1 graph-only
operator into a governed-actuation operator. This document defines
those slices: three independent, runtime-gated, additive changes that
preserve the current decision-graph + outcome-event contract and
extend it with `runtime.InvokeTool`.

The shape is mechanical: `runtimerollout.Operator`
(`internal/application/runtimerollout/operator.go`) is the canonical
template — `CreateSession` → `InvokeTool` → branch on `result.Status`
into completed / failed / escalated-after-denial. The three operators
in scope already produce the bounded decisions and parameters the
runtime tools accept; the missing seam is the runtime client wiring
plus a per-decision arguments mapper.

## Current state of the three operators

| Operator | Package | v1 decision space | v1 emits | Runtime call today |
|---|---|---|---|---|
| `saturation-operator` | `internal/application/saturationoperator/` | `{execute, escalate, reject}` over a planner-chosen `chosen_action ∈ {scale_up, restart_pods, circuit_break}` | `payments.incident.resource-saturation.operation.{completed,failed,escalated}` | none — graph-only |
| `payment-integrity-operator` | `internal/application/paymentintegrityoperator/` | `{compensate, replay, escalate, not_enough_evidence}` | `payments.incident.payment-integrity.{compensated,replayed,escalated,failed}` | none — graph-only |
| `human-escalation` | `internal/application/humanescalation/` | `{engage_owner, failed}` | `payments.incident.escalated.to-human` / `payments.incident.failed.terminal` | none — graph-only |

The existing graph waves stay untouched. Each post-runtime slice is
purely additive: add the runtime call, propagate `invocation_id` into
the existing decision-graph node properties + outcome payload, and
flip `runtime_execution.governed` to `true` plus `primary_tools` to
the matching list in `contracts/specialists/catalog.v1.yaml`.

## Three slices (independent, each gated on its own runtime tool set)

### Slice A — saturation-operator gains governed K8s actuation

**Trigger.** `k8s.scale_deployment`, `k8s.restart_pods`, and
`k8s.circuit_break` registered in `underpass-runtime`'s
`DefaultCapabilities()`, and the `saturation-operator-bounded` tool
profile recognised by the runtime's policy engine.

**Code surface (PIR).**

- `internal/application/saturationoperator/operator.go`
  - Add `runtime runtime.Client` field on `Operator` (mirror
    `runtimerollout.Operator.runtime`).
  - In the `execute` branch (today: graph wave + `operation.completed`),
    insert `CreateSession` → `InvokeTool` between the LLM decision and
    the outcome publish. `ToolName` comes from the planner's
    `chosen_action`:
    - `scale_up`        → `k8s.scale_deployment`
    - `restart_pods`    → `k8s.restart_pods`
    - `circuit_break`   → `k8s.circuit_break`
  - `Arguments` are forwarded from the plan node verbatim: planner's
    parameters are already shaped to the input schemas defined in
    `runtime-saturation-and-notify-tools-registration.md` §"Detailed
    capability specs". A thin per-action `mapPlanToArgs(...)` keeps
    field renames local (e.g. planner's `replicas_target` → tool's
    `replicas`).
  - `CreateSessionRequest`: `ToolProfile=saturation-operator-bounded`,
    `GovernanceProfile=saturation-operator-safe`,
    `SuccessProfile=saturation-plan-executed`,
    `Metadata{"runtime_environment": env.Environment}`.
  - Status branching mirrors `runtimerollout`:
    - `succeeded` → existing `operation.completed` path, plus
      `invocation_id` on payload + decision node.
    - `denied`    → new `operation.escalated` path with
      `denied_reason` (the same shape `runtimerollout.finishEscalatedAfterDenial`
      uses today).
    - `failed`    → existing `operation.failed` path with the runtime
      error reason.
  - Add `runtime.WithActor(ctx, "saturation-operator")` and
    `runtime.WithRoles(ctx, "specialist", "devops", "platform_admin")`
    — `circuit_break` is HIGH-risk in the proposal, same role bar as
    rollout.
- `cmd/server/main.go`
  - Already builds `runtime.Client` for `runtimerollout`. Reuse the
    same client for `saturationoperator.New(...)`. Opt-in rule
    matches: `RUNTIME_GRPC_ADDR` empty → fall back to a no-op client
    that returns `runtime client unconfigured` so the operator can
    still emit a deterministic `failed` outcome rather than hang.
- `contracts/specialists/catalog.v1.yaml`
  - `saturation-operator.tool_profile`:
    `saturation-operator-deferred` → `saturation-operator-bounded`.
  - `saturation-operator.runtime_execution.governed`: `false` →
    `true`.
  - `saturation-operator.runtime_execution.primary_tools`: `[]` →
    `[k8s.scale_deployment, k8s.restart_pods, k8s.circuit_break]`.
- `internal/events/v1/`
  - Add `ToolProfileSaturationOperatorBounded` constant alongside
    `ToolProfileRuntimeRolloutNarrow`. The conformance test will
    catch the catalog drift if this is missed.

**Acceptance.**

- `go test ./internal/application/saturationoperator/...` covers the
  three success paths (one per tool) + denied path + failed path
  against a fake `runtime.Client`.
- Catalog conformance test (`internal/events/v1`) passes after the
  Go-side constant lands.
- Coverage gate stays green (`>= 80%` per non-excluded package).
- Smoke (cluster) — see below.

**Smoke.** Reuse `docs/implementation-status.md` §"How to smoke
(saturation chain)". Pre-condition: a real Deployment in a namespace
the runtime SA can write to (`payments-api-fixture` in
`underpass-runtime` is already proven for rollout). Plant a saturation
alert, watch `payments.incident.resource-saturation.>` for three
envelopes ending in
`payments.incident.resource-saturation.operation.completed` whose
payload now carries `invocation_id`. Cross-check K8s side: the
fixture deployment's replicas / pod set changed as expected.

---

### Slice B — payment-integrity-operator gains governed ledger actuation

**Trigger.** `payments.compensate_payment` and
`payments.replay_payment` registered in `underpass-runtime` with the
six invariants in
`runtime-payment-integrity-tools-registration.md` §"Governance
invariants" enforced — including invariant 6 (finding cross-check) —
and the new `financial_impact: true` capability flag accepted by the
runtime's `CapabilityDescriptor`.

**Code surface (PIR).**

- `internal/application/paymentintegrityoperator/operator.go`
  - Add `runtime runtime.Client` field on `Operator`.
  - In the `compensate` branch: `CreateSession` → `InvokeTool` with
    `ToolName=payments.compensate_payment`. Arguments from the
    finding + decision: `payment_id`, `idempotency_key`,
    `incident_id`, `finding_node_id`, `decision_node_id`,
    optionally `amount_check` + `currency_check` from the snapshot
    evidence node.
  - In the `replay` branch: same shape with
    `ToolName=payments.replay_payment`. The runtime's invariant 6
    cross-checks the finding's classifications, so the operator does
    not need to re-validate `idempotency_safe` at the call site —
    the prompt rules already forbid the unsafe cells, and the
    runtime is defense in depth.
  - `escalate` and `not_enough_evidence`: unchanged graph-only path
    (no runtime hop on these branches).
  - `CreateSessionRequest`:
    `ToolProfile=payment-integrity-operator-bounded`,
    `GovernanceProfile=payment-integrity-operator-safe`,
    `SuccessProfile=payment-integrity-resolved`,
    `Metadata{"runtime_environment": env.Environment}`.
  - Status branching mirrors `runtimerollout`. `succeeded` → existing
    `compensated` / `replayed` path with `invocation_id` on payload
    + decision node. `denied` → `escalated` with `denied_reason`
    (and the denial sentinel surfaces in the LLM-authored handoff
    narrative human-escalation will read). `failed` → existing
    `failed` path.
  - Roles: `runtime.WithActor(ctx, "payment-integrity-operator")` +
    `runtime.WithRoles(ctx, "specialist", "payments_operator")`.
    `payments_operator` is the new role gate the runtime adds for
    FINANCIAL_IMPACT-class capabilities (open question 5 in the
    runtime proposal); confirm the role name post-merge.
- `cmd/server/main.go`
  - Pass the same `runtime.Client` into
    `paymentintegrityoperator.New(...)`. Same opt-in rule as
    saturation.
- `contracts/specialists/catalog.v1.yaml`
  - `payment-integrity-operator.runtime_execution.governed`: `false`
    → `true`.
  - `payment-integrity-operator.runtime_execution.primary_tools`:
    `[]` → `[payments.compensate_payment, payments.replay_payment]`.
  - The `tool_profile` value (`payment-integrity-operator-bounded`)
    already matches the proposal — no rename needed.
- `internal/events/v1/`
  - Constants for `ToolProfilePaymentIntegrityOperatorBounded` (or
    the catalog-equivalent value if a Go constant exists) and the
    new denial sentinels surfaced in `denied_reason`
    (`compensate_unsafe_side_effect_status`,
    `replay_unsafe_classifications`, etc.) so unit tests can assert
    against typed values rather than raw strings.

**Acceptance.**

- `go test ./internal/application/paymentintegrityoperator/...`
  covers compensate / replay success paths + denied path (with each
  of the new finding-cross-check sentinels) + failed path.
- Catalog conformance test passes.
- Coverage gate green.
- Smoke (cluster) — see below.

**Smoke.** Reuse `docs/implementation-status.md` §"How to smoke
(payment-integrity chain)". Pre-condition: a sandbox provider client
+ ledger client wired into the runtime (the runtime team's slice;
PIR cannot smoke ledger writes without this). Plant a stuck-payment
signal whose `last_known_status` + `has_callback_received` shape
forces a `compensate` (set `has_callback_received=false` and
`last_known_status=AUTHORIZED`); watch
`payments.incident.payment-integrity.>` for the
`compensated` envelope carrying `invocation_id` plus a `ledger_entry_id`
returned by the runtime. Cross-check the sandbox ledger: a single
reversal entry exists with the same `idempotency_key`. **Negative
smoke**: re-publish the same incident envelope; the second invocation
must return `compensation_status=no_op_already_compensated`
(GUARANTEED idempotency on `idempotency_key`) and the ledger must
NOT gain a second entry.

---

### Slice C — human-escalation gains governed channel notification

**Trigger.** `notify.escalation_channel` registered in
`underpass-runtime` with the `human-escalation-minimal` tool profile
recognised by the policy engine, plus the per-`incident_id` rate
limit (invariant 6 in the runtime proposal).

**Code surface (PIR).**

- `internal/application/humanescalation/escalator.go`
  - Add `runtime runtime.Client` field on the escalator.
  - In the `engage_owner` branch (today: handoff graph wave +
    `payments.incident.escalated.to-human`), insert
    `CreateSession` → `InvokeTool` with
    `ToolName=notify.escalation_channel`. Arguments from the upstream
    envelope + LLM-authored handoff narrative:
    `incident_id`, `handoff_node_id`, `summary` (the LLM's one-line
    handoff summary, capped at 200 chars), `upstream_specialist`
    (extracted from the consumed subject), `upstream_decision`,
    `reason` (LLM rationale, capped at 1000 chars), optional
    `resource_ref`.
  - The `failed` branch stays graph-only (no notify hop on
    `failed.terminal`).
  - `CreateSessionRequest`:
    `ToolProfile=human-escalation-minimal`,
    `GovernanceProfile=human-escalation-safe`,
    `SuccessProfile=human-owner-engaged`,
    `Metadata{"runtime_environment": env.Environment}`.
  - Status branching is simpler than the K8s tools:
    - `succeeded` → `escalated.to-human` with `notify_delivered=true`
      and `notify_channel` from `result.Output`.
    - `denied` (rate-limited) → still emit `escalated.to-human`
      because the kernel handoff node is the source of truth; the
      payload carries `notify_delivered=false` and `denied_reason`
      so dashboards can flag suppressed deliveries.
    - `failed`  → still emit `escalated.to-human` with
      `notify_delivered=false` and the runtime error. **Rationale:**
      the human-escalation contract is "the handoff exists in the
      kernel"; channel delivery is best-effort. Failing to notify
      must NOT block the terminal outcome.
  - Roles:
    `runtime.WithActor(ctx, "human-escalation")` +
    `runtime.WithRoles(ctx, "specialist", "incident_communicator")`.
    `incident_communicator` is the proposed role for `EXTERNAL`-scope
    notification capabilities (open question — confirm with runtime).
- `cmd/server/main.go`
  - Pass the runtime client into `humanescalation.New(...)`.
  - Opt-in rule: `RUNTIME_GRPC_ADDR` empty → escalator uses a no-op
    runtime client that always returns `failed`, so the terminal
    event still publishes with `notify_delivered=false`. **The
    human-escalation subscription must NOT skip on missing runtime**
    — the LLM is what gates it (already enforced), and the kernel
    handoff node is the contract.
- `contracts/specialists/catalog.v1.yaml`
  - `human-escalation.runtime_execution.governed`: `false` → `true`.
  - `human-escalation.runtime_execution.primary_tools`: `[]` →
    `[notify.escalation_channel]`.
  - The `tool_profile` value (`human-escalation-minimal`) already
    matches — no rename needed.
- `internal/events/v1/`
  - Extend `EscalatedToHumanPayload` (or the matching type) with
    optional `NotifyDelivered bool` + `NotifyChannel string` +
    `DeniedReason string` so consumers can distinguish a delivered
    handoff from a kernel-only handoff.

**Acceptance.**

- `go test ./internal/application/humanescalation/...` covers
  `engage_owner` happy path (notify succeeded), denied path (rate
  limit), failed path (transport error), and `failed` decision
  (no notify hop). Each path emits exactly one terminal envelope.
- Coverage gate green.
- Smoke (cluster) — see below.

**Smoke.** Reuse `docs/implementation-status.md` §"How to smoke
(human-escalation chain)" with one addition: subscribe a test
listener on the configured Slack/Pager channel (or capture via the
runtime's `workspace.events.invocation.completed` event). Plant a
forced `runtime-rollout.escalated`; expect:
1. `payments.incident.escalated.to-human` envelope with
   `notify_delivered=true`, `notify_channel=<resolved channel>`.
2. A delivered message on the test channel containing the
   `handoff_node_id` + summary.
3. `workspace.events.invocation.completed` from the runtime tying
   the invocation back to PIR's `correlation_id`.
**Rate-limit negative smoke**: replay the escalation envelope twice
within 60 s. The second emission carries `notify_delivered=false`
+ `denied_reason=rate_limit_exceeded`; the kernel still has a
single handoff node (not two — the consumer is idempotent on
`incident_run_id`).

---

## Sequencing

The three slices are **independent** at the code level — each touches
only its own operator package and a non-overlapping section of the
catalog. Order is determined by which runtime tool lands first:

1. **Slice C (human-escalation / notify) is the cheapest** — one tool,
   one decision branch, no parameter mapping. Recommended first
   landing once `notify.escalation_channel` ships, both as a smaller
   shake-out of the runtime-side enforcement and because it
   immediately closes the visible-to-humans gap (no more "navigate
   to the kernel handoff node manually").
2. **Slice A (saturation) is the next cheapest** — three tools, but
   all REVERSIBLE / MEDIUM risk and the planner already chose the
   action. Mostly mechanical mapping work.
3. **Slice B (payment-integrity) has the highest blast radius** —
   FINANCIAL_IMPACT, GUARANTEED idempotency on a real key, six
   invariants including the new finding cross-check. Worth landing
   last so the runtime-side enforcement has time to harden against
   the simpler tools first. The negative smoke (double-publish,
   verify ledger only gains one entry) is the slice's load-bearing
   test.

If the runtime team lands all the tools simultaneously, the same
order still applies on the PIR side because the cost / risk
gradient is what matters.

## Cross-cutting work (one-shot, before any of the three slices)

Two small changes the three slices share. Either land them as a
fourth bootstrap slice, or fold into Slice C since it lands first:

- **Promote `runtime.Client` from a single-consumer field to a shared
  dependency.** Today `cmd/server/main.go` builds the client once and
  passes it only to `runtimerollout.New(...)`. The three slices need
  the same client. Restructure the build / wiring so it is
  constructed once and passed to all four operators (rollout,
  saturation, payment-integrity, human-escalation) and degrade
  gracefully when `RUNTIME_GRPC_ADDR` is empty.
- **Codify the per-specialist actor / roles helper.** Today
  `runtimerollout` calls `runtime.WithActor` + `runtime.WithRoles`
  inline. Either keep that pattern (one inline pair per operator
  package — fine, simple) or extract `runtime.SpecialistContext(ctx,
  specialistID, roles...)` if the second + third operator both repeat
  the same shape. **Decision deferred to Slice C** — pick once a real
  duplication exists, not preemptively.

## Open questions

1. **Role names for FINANCIAL_IMPACT and EXTERNAL scopes.** The
   rollout proposal established `specialist + devops +
   platform_admin` as the role tuple for CLUSTER + HIGH risk. The
   payment-integrity proposal does not name roles; this plan
   suggests `payments_operator` for FINANCIAL_IMPACT and
   `incident_communicator` for EXTERNAL notify. Confirm with the
   runtime team before Slice B / Slice C land.
2. **Argument mapper location.** Each slice has a small per-tool
   argument mapper (e.g. saturation's `mapPlanToArgs`). Keep them
   inside each operator package (one mapper per slice, no shared
   surface) or extract to a `internal/adapters/runtime/args/`
   sub-package? **Default: keep inline** — three small mappers do
   not justify shared infrastructure and the operators have no
   reason to share argument shapes.
3. **Catalog `runtime_execution.governed` flip.** Each catalog edit
   is a one-line YAML change but flipping `governed: true` is a
   contract change visible to anyone who reads the catalog as the
   source of truth for what runs governed. Worth landing in the
   same commit as the operator change so the catalog never claims
   `governed: true` for code that does not actually call the
   runtime yet.

## Non-goals for these slices

- **Tool recommendation flow.** None of the three operators uses
  `runtime.RecommendTools` today; their decisions come from the LLM
  + planner, not from the runtime's recommender. Out of scope unless
  a separate slice asks for it.
- **Cross-pipeline invocation correlation.** The runtime's
  `correlation_id` already ties invocation events back to PIR
  envelopes. No additional bridging needed.
- **`payments.get_*` read tools.** Listed in
  `runtime-payment-integrity-tools-registration.md` as future v2
  optional reads. The investigators reason from the kernel bundle +
  watchdog signal alone today; out of scope here.
- **`k8s.circuit_break` mesh translation.** PIR forwards whatever the
  planner produced; the runtime owns the mesh-CRD translation
  (Istio / Linkerd / NetworkPolicy fallback). PIR should not start
  encoding mesh kinds in arguments.

## What this plan does not include

- The three slices are runtime-gated. **No PIR code lands until the
  matching runtime tool registration ships.** This document is the
  plan, not a parallel implementation.
- Until the slices land, the v1 graph-only paths remain the
  deterministic substitute. They are correct and complete; they just
  do not actuate. `docs/implementation-status.md` already records
  this in §"Pending debt and follow-ups" → "Architectural /
  operational".
