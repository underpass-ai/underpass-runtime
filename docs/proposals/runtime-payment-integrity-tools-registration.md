# Proposal — Register payment-integrity tools in `underpass-runtime`

Status: proposed, awaiting runtime team review
Source repo: `underpass-payments-incident-response`
Target repo: `underpass-runtime`
Owner of the request: PIR
Date: 2026-04-25

## Summary

PIR's `payment-integrity-operator` (the second hop of the
payment-integrity 2-in-series pipeline, status `implemented` in
`contracts/specialists/catalog.v1.yaml`) decides between four bounded
actions on a stuck-payment incident: `compensate`, `replay`,
`escalate`, `not_enough_evidence`. v1 records the decision as a graph
wave and emits the matching outcome event, but **does not invoke the
runtime** because the two write tools the operator needs do not
exist in `underpass-runtime` today and warrant a different governance
profile from anything currently registered.

This proposal asks the runtime team to register two new
**FINANCIAL_IMPACT-class** tools — `payments.compensate_payment` and
`payments.replay_payment` — and the matching governance enforcement.
Unlike K8s rollback or scaling tools (REVERSIBLE / IRREVERSIBLE on
infrastructure), these tools touch ledger state and provider APIs;
the policy layer must enforce harder invariants because the
consequences of misuse are double-charges, double-refunds, or
ledger drift, and those are not safely recoverable in the runtime
loop.

## Motivation

Stuck-payment incidents are the most consequential incident class
PIR handles today. The pipeline already converges on the right
decision in v1: kernel-rich finding that classifies the payment
along four dimensions (`downstream_side_effect_status`,
`idempotency_safe`, `callback_received`, `financial_impact_class`),
LLM operator that picks a bounded compensate/replay/escalate decision
under hard prompt rules. What is missing is the seam between PIR's
governed decision and the actual ledger / provider mutation. Without
it, every diagnosed stuck payment ends in either a manual
reconciliation (the human owns the reverse-channel, no automation)
or an in-graph decision that nobody acts on.

The shape mirrors the rollout-tools and saturation-tools proposals
(`docs/proposals/runtime-rollout-tools-registration.md` and
`docs/proposals/runtime-saturation-and-notify-tools-registration.md`).
This one diverges on three points: HIGH/FINANCIAL_IMPACT risk level,
GUARANTEED idempotency keyed on the upstream payment's
idempotency_key (not on tool-side bookkeeping), and a six-invariant
policy block where invariant 6 is the strongest the runtime has
asked for so far (a tool-side cross-check against the upstream
finding's classifications).

## Current state in runtime

`grep -rn 'payments\.' internal/adapters/tools/` in `underpass-runtime`
returns nothing — there is no `payments.*` namespace today. PIR's
consumption side:

| Tool required by PIR contract | Status in runtime | Resolution |
|---|---|---|
| `payments.compensate_payment` | **Missing** (new namespace) | Register new tool |
| `payments.replay_payment`     | **Missing**                | Register new tool |

Optional read tools that would let the investigator validate state
post-handoff (out of scope for this proposal — the v1 investigator
reasons over the watchdog signal + kernel bundle alone, no runtime
read calls):

| Tool | Status | Notes |
|---|---|---|
| `payments.get_payment_record` | Missing | future v2; lets investigator re-check status when bundle is stale |
| `payments.get_idempotency_log` | Missing | future v2; confirms idempotency_safe classification |
| `payments.get_provider_callback_log` | Missing | future v2; resolves callback_received ambiguity |

## Target state

`DefaultCapabilities` registers the two new write tools with the
shapes in §"Detailed capability specs". The policy engine enforces
the six invariants in §"Governance invariants" before any write
reaches the ledger or the provider. A new tool profile binds them:

- `payment-integrity-operator-bounded` — replaces the
  `payment-integrity-operator-bounded` placeholder in PIR's
  specialist catalog (already named there but with empty
  `primary_tools`); becomes non-empty by binding the two tools.

A new governance profile is registered:

- `payment-integrity-operator-safe` — wraps the six invariants. The
  catalog already names this profile; the runtime side is the
  enforcement.

## Detailed capability specs

Both tools share: scope `EXTERNAL` (the work happens at the ledger /
provider boundary, not at K8s), a `PolicyMetadata` block requiring
`tool_profile=payment-integrity-operator-bounded`, `trace_name` /
`span_name` matching the tool name. Both REQUIRE `approved=true` per
invocation.

### 1. `payments.compensate_payment` (new, write — FINANCIAL_IMPACT)

Issues a reverse / refund against the payment provider for a payment
the operator has classified as `downstream_side_effect_status =
known_not_applied`. The tool MUST verify against the provider
(idempotency key + payment_id) before mutating the ledger; the
runtime owns this verification step.

```yaml
name: payments.compensate_payment
description: |
  Reverse / refund a stuck payment whose downstream side-effect was
  confirmed NOT applied. The tool resolves the provider client from
  payment_id metadata, verifies the provider has no record of the
  payment beyond DECLINED, and only then writes the reversal entry
  to the ledger. Idempotent on the provider's idempotency_key:
  re-issuing the same call MUST be a no-op even if the first attempt
  partially completed.
scope: EXTERNAL
risk_level: HIGH
side_effects: IRREVERSIBLE
financial_impact: true
requires_approval: true
idempotency: GUARANTEED   # keyed on idempotency_key
input_schema: |
  {
    "type": "object",
    "required": ["payment_id", "idempotency_key", "incident_id", "finding_node_id", "decision_node_id"],
    "properties": {
      "payment_id":       {"type": "string", "maxLength": 200},
      "idempotency_key":  {"type": "string", "maxLength": 200},
      "incident_id":      {"type": "string", "maxLength": 200},
      "finding_node_id":  {"type": "string", "maxLength": 200},
      "decision_node_id": {"type": "string", "maxLength": 200},
      "amount_check":     {"type": "string", "maxLength": 64},
      "currency_check":   {"type": "string", "maxLength": 8}
    },
    "additionalProperties": false
  }
output_schema: |
  {
    "type": "object",
    "required": ["payment_id", "compensation_status", "ledger_entry_id"],
    "properties": {
      "payment_id":          {"type": "string"},
      "compensation_status": {"enum": ["compensated", "no_op_already_compensated", "rejected_provider_state"]},
      "ledger_entry_id":     {"type": "string"},
      "provider_reference":  {"type": "string"},
      "reversed_amount":     {"type": "string"},
      "reversed_currency":   {"type": "string"}
    }
  }
constraints:
  timeout_seconds: 60
  max_retries: 0   # idempotency is keyed; the runtime never retries for us
  output_limit_kb: 16
policy:
  arg_fields:
    - {field: "payment_id",       max_length: 200}
    - {field: "idempotency_key",  max_length: 200}
    - {field: "incident_id",      max_length: 200}
    - {field: "finding_node_id",  max_length: 200}
    - {field: "decision_node_id", max_length: 200}
  simple_fields:
    - "tool_profile"   # must equal "payment-integrity-operator-bounded"
    - "environment"
observability:
  trace_name: "payments.compensate_payment"
  span_name:  "invoke_payments_compensate_payment"
```

### 2. `payments.replay_payment` (new, write — FINANCIAL_IMPACT)

Re-issues a stuck payment using the SAME idempotency_key. The
provider's idempotency layer guarantees no double-effect: if the
previous attempt completed downstream the provider returns the same
response without re-charging; if it did not, the new attempt
completes normally.

```yaml
name: payments.replay_payment
description: |
  Re-issue a stuck payment using its original idempotency_key. The
  tool resolves the provider client from payment_id metadata and
  POSTs the same operation to the provider with the same key. Safe
  ONLY when the upstream finding classified
  downstream_side_effect_status=ambiguous AND idempotency_safe=yes;
  the policy engine enforces this contract by reading the finding
  attached to the request.
scope: EXTERNAL
risk_level: HIGH
side_effects: IRREVERSIBLE
financial_impact: true
requires_approval: true
idempotency: GUARANTEED   # keyed on idempotency_key
input_schema: |
  {
    "type": "object",
    "required": ["payment_id", "idempotency_key", "incident_id", "finding_node_id", "decision_node_id"],
    "properties": {
      "payment_id":       {"type": "string", "maxLength": 200},
      "idempotency_key":  {"type": "string", "maxLength": 200},
      "incident_id":      {"type": "string", "maxLength": 200},
      "finding_node_id":  {"type": "string", "maxLength": 200},
      "decision_node_id": {"type": "string", "maxLength": 200}
    },
    "additionalProperties": false
  }
output_schema: |
  {
    "type": "object",
    "required": ["payment_id", "replay_status"],
    "properties": {
      "payment_id":         {"type": "string"},
      "replay_status":      {"enum": ["replayed_completed", "replayed_idempotent_match", "replayed_failed"]},
      "provider_reference": {"type": "string"},
      "final_status":       {"type": "string"}
    }
  }
constraints:
  timeout_seconds: 60
  max_retries: 0
  output_limit_kb: 16
policy:
  arg_fields:
    - {field: "payment_id",       max_length: 200}
    - {field: "idempotency_key",  max_length: 200}
    - {field: "incident_id",      max_length: 200}
    - {field: "finding_node_id",  max_length: 200}
    - {field: "decision_node_id", max_length: 200}
  simple_fields:
    - "tool_profile"
    - "environment"
observability:
  trace_name: "payments.replay_payment"
  span_name:  "invoke_payments_replay_payment"
```

## Governance invariants

The runtime's policy engine MUST enforce the following before either
write reaches the provider or the ledger:

1. **Tool profile match.** Session metadata `tool_profile` must equal
   `payment-integrity-operator-bounded`. Anything else (including
   missing) → `DENIED` with sentinel `tool_profile_mismatch`.
2. **Environment match.** Session metadata `environment` must equal
   the runtime's notion of the current environment. Mismatch →
   `DENIED` with `environment_mismatch`.
3. **Approval flag.** Both tools have `requires_approval: true`. PIR
   sets `InvokeToolRequest.approved=true` per invocation. Without it
   → `DENIED` with `approval_required`.
4. **Idempotency_key bind.** The same `idempotency_key` MUST NOT be
   used for `compensate_payment` AND `replay_payment` within the
   runtime's bookkeeping window (24h default). Cross-tool reuse →
   `DENIED` with `idempotency_cross_tool_reuse`. Re-issuing the
   *same* tool with the same key is the GUARANTEED-idempotent path
   and proceeds.
5. **Decision-node provenance.** The `decision_node_id` field must
   reference a kernel decision node whose `source_agent` is
   `payment-integrity-operator` and whose `operator_decision`
   property matches the tool being invoked (`compensate` for
   `compensate_payment`, `replay` for `replay_payment`). The
   runtime resolves the decision via the kernel's GetNodeDetail or
   a future tool-side kernel client; mismatch → `DENIED` with
   `decision_provenance_mismatch`. PIR cannot invent decisions.
6. **Finding cross-check (FINANCIAL_IMPACT).** The runtime fetches
   the finding referenced by `finding_node_id` and verifies its
   classifications match the tool's preconditions. Specifically:
   - For `compensate_payment`: finding's
     `downstream_side_effect_status` MUST equal `known_not_applied`.
     `known_applied` or `ambiguous` → `DENIED` with
     `compensate_unsafe_side_effect_status`.
   - For `replay_payment`: finding's
     `downstream_side_effect_status` MUST be `ambiguous` AND
     `idempotency_safe` MUST be `yes`. Anything else → `DENIED`
     with `replay_unsafe_classifications`.
   The PIR-side prompt rules forbid these decisions in the unsafe
   shapes already, but the runtime-side cross-check is defense in
   depth: a misbehaving LLM cannot bypass it.

Denials materialize as `Invocation{status: DENIED, error: {...}}`
with the sentinel above. PIR's operator already follows the contract
contract `runtime-rollout-operator` consumes — same shape, different
sentinels.

## Acceptance criteria

- `DefaultCapabilities()` in `internal/adapters/tools/` registers the
  two tools with the shapes in §"Detailed capability specs".
- A new payment-integrity tool implementation file (e.g.
  `internal/adapters/tools/payment_integrity_tools.go`) wraps the
  payment-provider client and the ledger client. PIR is fine with
  in-process or sidecar shape; the contract is the InvokeTool seam.
- Unit tests in `internal/adapters/tools/` cover the happy path for
  each tool against fakes (provider client + ledger client) and the
  six invariants in §"Governance invariants" with the named sentinel
  codes.
- `EventInvocationCompleted` / `EventInvocationDenied` events on
  `workspace.events.invocation.{completed,denied}` carry the
  `correlation_id` PIR passes in `InvokeToolRequest`. The
  `payment_id` MUST also be added as a structured field on the
  invocation event so audit can group by transaction.
- The `payment-integrity-operator-bounded` tool profile and the
  `payment-integrity-operator-safe` governance profile are documented
  somewhere PIR can reference.
- The `financial_impact: true` capability flag (new) is added to
  the runtime's CapabilityDescriptor. PIR's downstream observability
  uses it to apply stricter alerting (e.g. an `Invocation{status:
  COMPLETED, financial_impact: true}` is paged on every event in
  staging, not sampled).

## Open questions for the runtime team

1. **Provider client config.** Where does the provider config live
   (Stripe / Adyen / etc.)? PIR proposes the runtime owns the secret
   + config and resolves it from `payment_id` metadata; PIR never
   ships secrets. Confirm.
2. **Ledger client.** Same question for the ledger. PIR's expectation
   is that the runtime maintains a single ledger client per
   environment and applies entries with idempotency_key as the
   dedupe column. Acceptable?
3. **Kernel cross-check.** Invariant 6 needs the runtime to fetch
   the finding from the kernel. Does the runtime want a direct
   kernel client or should the operator pass the finding's
   classifications inline as input fields (less defense-in-depth but
   removes the runtime's kernel dependency)?
4. **Idempotency window.** Invariant 4 needs persisted state. PIR
   suggests 24h default, configurable. Acceptable to keep it in
   process memory if the runtime is single-replica today, with a
   follow-up to move to a shared KV when the runtime scales out?
5. **financial_impact: true rollout.** This is a new capability flag
   that the runtime team would adopt for any future
   FINANCIAL_IMPACT-class tool (notifications, accounting,
   payouts). Are you OK introducing it as a first-class field
   instead of an ad hoc label?

## PIR-side readiness

The PIR work landing in step 1 of this slice (already merged) is
fully fakeable — the operator emits the bounded outcome subject and
the decision graph wave with the parameters the runtime needs to
verify (idempotency_key, finding_node_id, decision_node_id). The
post-merge follow-up on the PIR side, gated on this proposal:

- **Operator runtime invocation.** Extend
  `internal/application/paymentintegrityoperator` with a runtime
  client (mirroring `runtimerollout`'s shape) and a thin per-decision
  branch that translates `compensate` /`replay` to the matching
  `runtime.InvokeTool` call. The decision graph + outcome event stay
  identical; the change is purely additive.
- **Specialist catalog update.** Move
  `payment-integrity-operator-bounded`'s `primary_tools` from `[]`
  to `[payments.compensate_payment, payments.replay_payment]`.

Cluster end-to-end of payment-integrity actuation waits on this
proposal merging. Until then, the v1 graph-only operator is the
deterministic substitute — every diagnosed stuck payment converges
on a bounded decision recorded in the kernel and an outcome event
that human-escalation consumes when the operator picks `escalate`.
