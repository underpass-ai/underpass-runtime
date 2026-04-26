# Proposal — Register saturation + escalation-notify tools in `underpass-runtime`

Status: proposed, awaiting runtime team review
Source repo: `underpass-payments-incident-response`
Target repo: `underpass-runtime`
Owner of the request: PIR
Date: 2026-04-25

## Summary

PIR has two convergence points that need governed write tools the
runtime does not register today:

1. **`saturation-operator`** (third hop of the resource-saturation
   3-in-series pipeline, status `implemented` in `contracts/specialists/catalog.v1.yaml`)
   converges on a plan whose `chosen_action` is one of `scale_up`,
   `restart_pods`, `circuit_break`. The operator currently records its
   decision as a graph wave and emits an outcome event, but cannot
   actuate; the catalog flags this as known v1 debt.
2. **`human-escalation`** (status `implemented`, lands in this branch)
   converges on `engage_owner` and emits the catalog terminal
   `payments.incident.escalated.to-human`. v1 has no notification
   surface, so the human owner only sees the handoff if they navigate
   to the kernel handoff node manually.

This proposal asks the runtime team to register **three new K8s
write tools** (`k8s.scale_deployment`, `k8s.restart_pods`,
`k8s.circuit_break`) and **one new notification tool**
(`notify.escalation_channel`), and to enforce the bounded invariants
in §"Governance invariants" at the policy layer.

The shape mirrors the rollout-tools proposal
(`docs/proposals/runtime-rollout-tools-registration.md`, accepted /
pending merge). Where the rollout proposal asks for read tools to
support investigation and write tools to support a four-value decision
space, this proposal is purely write-side: PIR's investigators and
planners read what they need from the kernel via `GetContext`, not
from the runtime.

## Motivation

The seven-stage flow has stages 4 (tool suggestion + policy check) and
5 (governed execution) closed for `runtime-rollout-operator` and only
for that specialist. The saturation pipeline reaches `decision`
without a runtime hop; the human-escalation specialist reaches its
terminal outcome without a notify hop. Both pipelines therefore stop
short of the architecture's "bounded specialist that actuates through
the runtime" pattern. Registering these four tools is what closes the
gap without redesigning anything: each operator already produces a
typed decision with parameters; the runtime just needs to expose the
verbs and enforce the invariants.

## Current state in runtime

`grep -rn '"k8s\.' internal/adapters/tools/` in `underpass-runtime`
enumerates the K8s capabilities registered today:

```
k8s.apply_manifest
k8s.get_deployments
k8s.get_images
k8s.get_logs
k8s.get_pods
k8s.get_services
k8s.local
k8s.restart_deployment
k8s.rollout_status
k8s.set_image
```

Notification tools today: none. PIR's consumption side:

| Tool required by PIR contract | Status in runtime | Resolution |
|---|---|---|
| `k8s.scale_deployment`        | **Missing** | Register new tool |
| `k8s.restart_pods`            | **Naming drift** — runtime has `k8s.restart_deployment` (deployment-level rollout restart) | Register a sibling at pod scope, or alias — see §"Naming drift" |
| `k8s.circuit_break`           | **Missing** | Register new tool |
| `notify.escalation_channel`   | **Missing** (new namespace) | Register new tool |

## Target state

`DefaultCapabilities` registers four new tools with the shapes in
§"Detailed capability specs". The policy engine enforces the
invariants in §"Governance invariants" before any write reaches the
target system (K8s API or external notification provider). Two new
tool profiles bind these capabilities:

- `saturation-operator-bounded` — supersedes the `saturation-operator-deferred`
  placeholder in PIR's specialist catalog; binds the three K8s
  saturation tools.
- `human-escalation-minimal` — already named in PIR's catalog;
  becomes non-empty by binding `notify.escalation_channel`.

## Detailed capability specs

All four new tools share this common shape: scope `CLUSTER` for the
K8s tools and `EXTERNAL` for the notify tool, a `PolicyMetadata`
block requiring the matching `tool_profile` session-metadata key, and
`trace_name` / `span_name` matching the tool name.

### 1. `k8s.scale_deployment` (new, write)

Equivalent to `kubectl scale deployment/{name} -n {namespace}
--replicas={n}`. The tool accepts either an absolute `replicas` value
or a relative `replicas_delta`; exactly one must be present.

```yaml
name: k8s.scale_deployment
description: |
  Set Deployment.spec.replicas to an absolute target or apply a
  bounded delta. Idempotent on the absolute path (a no-op when the
  desired count already matches). The relative path is one-shot per
  invocation; PIR's saturation-planner produces parameters the
  operator forwards verbatim.
scope: CLUSTER
risk_level: MEDIUM
side_effects: REVERSIBLE
requires_approval: true
idempotency: GUARANTEED   # absolute path; delta path is BEST_EFFORT
input_schema: |
  {
    "type": "object",
    "required": ["namespace", "deployment_name"],
    "oneOf": [
      {"required": ["replicas"]},
      {"required": ["replicas_delta"]}
    ],
    "properties": {
      "namespace":       {"type": "string", "maxLength": 253},
      "deployment_name": {"type": "string", "maxLength": 253},
      "replicas":        {"type": "integer", "minimum": 0, "maximum": 200},
      "replicas_delta":  {"type": "integer", "minimum": -50, "maximum": 50}
    },
    "additionalProperties": false
  }
output_schema: |
  {
    "type": "object",
    "required": ["namespace", "deployment_name", "previous_replicas", "target_replicas"],
    "properties": {
      "namespace":          {"type": "string"},
      "deployment_name":    {"type": "string"},
      "previous_replicas":  {"type": "integer"},
      "target_replicas":    {"type": "integer"},
      "applied":            {"type": "boolean"},
      "observed_generation":{"type": "integer"}
    }
  }
constraints:
  timeout_seconds: 30
  max_retries: 0
  output_limit_kb: 16
policy:
  arg_fields:
    - {field: "namespace",       max_length: 253}
    - {field: "deployment_name", max_length: 253}
    - {field: "replicas",        min: 0, max: 200}
    - {field: "replicas_delta",  min: -50, max: 50}
  simple_fields:
    - "tool_profile"   # must equal "saturation-operator-bounded"
    - "environment"    # must equal session metadata's environment
observability:
  trace_name: "k8s.scale_deployment"
  span_name:  "invoke_k8s_scale_deployment"
```

### 2. `k8s.restart_pods` (new, write)

Equivalent to `kubectl rollout restart deployment/{name} -n {namespace}`
when invoked at deployment scope, or to deleting pods matching a
label selector when invoked at label-selector scope. The plan from
saturation-planner names exactly one mode.

```yaml
name: k8s.restart_pods
description: |
  Restart pods belonging to a Deployment by triggering a rollout
  restart annotation, or by deleting pods matching a label selector
  inside the Deployment. The label-selector mode is intended for
  partial drains (a single noisy replica) and is bounded to deleting
  at most max_pods pods per invocation.
scope: CLUSTER
risk_level: MEDIUM
side_effects: REVERSIBLE
requires_approval: true
idempotency: BEST_EFFORT
input_schema: |
  {
    "type": "object",
    "required": ["namespace", "deployment_name", "mode"],
    "properties": {
      "namespace":       {"type": "string", "maxLength": 253},
      "deployment_name": {"type": "string", "maxLength": 253},
      "mode":            {"enum": ["rollout_restart", "label_selector"]},
      "label_selector":  {"type": "string", "maxLength": 1024},
      "max_pods":        {"type": "integer", "minimum": 1, "maximum": 5}
    },
    "additionalProperties": false,
    "allOf": [
      {
        "if": {"properties": {"mode": {"const": "label_selector"}}},
        "then": {"required": ["label_selector", "max_pods"]}
      }
    ]
  }
output_schema: |
  {
    "type": "object",
    "required": ["namespace", "deployment_name", "mode", "pods_affected"],
    "properties": {
      "namespace":       {"type": "string"},
      "deployment_name": {"type": "string"},
      "mode":            {"type": "string"},
      "pods_affected":   {"type": "integer"},
      "rollout_revision":{"type": "integer"}
    }
  }
constraints:
  timeout_seconds: 30
  max_retries: 0
  output_limit_kb: 16
policy:
  arg_fields:
    - {field: "namespace",       max_length: 253}
    - {field: "deployment_name", max_length: 253}
    - {field: "label_selector",  max_length: 1024}
    - {field: "max_pods",        min: 1, max: 5}
  simple_fields:
    - "tool_profile"   # must equal "saturation-operator-bounded"
    - "environment"
observability:
  trace_name: "k8s.restart_pods"
  span_name:  "invoke_k8s_restart_pods"
```

### 3. `k8s.circuit_break` (new, write)

Apply a traffic-shed policy at the Service / VirtualService / Gateway
boundary. The exact CRD depends on the cluster's mesh choice; the
proposed shape stays mesh-agnostic by carrying `target_service` +
`downstream` and asking the runtime to translate to whatever CRD the
cluster recognises (Istio `VirtualService`, Linkerd `ServiceProfile`,
or a NetworkPolicy fallback).

```yaml
name: k8s.circuit_break
description: |
  Install or update a traffic-shed policy that blocks traffic from a
  named upstream Service to a named downstream destination, applied
  at the cluster's mesh / network layer. The policy is applied with
  a TTL so it auto-removes if not renewed; PIR records the policy id
  in the operator's decision graph so the human owner can observe and
  remove it after recovery.
scope: CLUSTER
risk_level: HIGH
side_effects: REVERSIBLE
requires_approval: true
idempotency: BEST_EFFORT
input_schema: |
  {
    "type": "object",
    "required": ["namespace", "target_service", "downstream", "ttl_seconds"],
    "properties": {
      "namespace":      {"type": "string", "maxLength": 253},
      "target_service": {"type": "string", "maxLength": 253},
      "downstream":     {"type": "string", "maxLength": 253},
      "ttl_seconds":    {"type": "integer", "minimum": 60, "maximum": 1800}
    },
    "additionalProperties": false
  }
output_schema: |
  {
    "type": "object",
    "required": ["namespace", "target_service", "downstream", "policy_id", "expires_at"],
    "properties": {
      "namespace":      {"type": "string"},
      "target_service": {"type": "string"},
      "downstream":     {"type": "string"},
      "policy_id":      {"type": "string"},
      "expires_at":     {"type": "string", "format": "date-time"},
      "mesh_kind":      {"type": "string"}
    }
  }
constraints:
  timeout_seconds: 60
  max_retries: 0
  output_limit_kb: 16
policy:
  arg_fields:
    - {field: "namespace",      max_length: 253}
    - {field: "target_service", max_length: 253}
    - {field: "downstream",     max_length: 253}
    - {field: "ttl_seconds",    min: 60, max: 1800}
  simple_fields:
    - "tool_profile"   # must equal "saturation-operator-bounded"
    - "environment"
observability:
  trace_name: "k8s.circuit_break"
  span_name:  "invoke_k8s_circuit_break"
```

### 4. `notify.escalation_channel` (new, write — external)

Post a structured handoff to the configured human-escalation channel.
v1 targets a Slack incoming webhook; subsequent versions can fan out
to PagerDuty / Opsgenie / etc. The runtime owns the credential and
the channel routing; PIR passes only the handoff content.

```yaml
name: notify.escalation_channel
description: |
  Notify the configured human-escalation channel with a structured
  handoff message. The runtime resolves channel / webhook by the
  session's environment + a runtime-side routing config; PIR never
  ships credentials or URLs. The notification carries a stable
  handoff_node_id so the receiver can pivot to the kernel for the
  full narrative.
scope: EXTERNAL
risk_level: LOW
side_effects: IRREVERSIBLE
requires_approval: true
idempotency: BEST_EFFORT
input_schema: |
  {
    "type": "object",
    "required": ["incident_id", "handoff_node_id", "summary", "upstream_specialist", "upstream_decision", "reason"],
    "properties": {
      "incident_id":         {"type": "string", "maxLength": 200},
      "handoff_node_id":     {"type": "string", "maxLength": 200},
      "summary":             {"type": "string", "maxLength": 200},
      "upstream_specialist": {"type": "string", "maxLength": 100},
      "upstream_decision":   {"type": "string", "maxLength": 100},
      "reason":              {"type": "string", "maxLength": 1000},
      "resource_ref":        {"type": "string", "maxLength": 200}
    },
    "additionalProperties": false
  }
output_schema: |
  {
    "type": "object",
    "required": ["delivered", "channel"],
    "properties": {
      "delivered":      {"type": "boolean"},
      "channel":        {"type": "string"},
      "provider":       {"type": "string"},
      "provider_msg_id":{"type": "string"}
    }
  }
constraints:
  timeout_seconds: 10
  max_retries: 1
  output_limit_kb: 4
policy:
  arg_fields:
    - {field: "incident_id",     max_length: 200}
    - {field: "handoff_node_id", max_length: 200}
    - {field: "summary",         max_length: 200}
    - {field: "reason",          max_length: 1000}
  simple_fields:
    - "tool_profile"   # must equal "human-escalation-minimal"
    - "environment"
observability:
  trace_name: "notify.escalation_channel"
  span_name:  "invoke_notify_escalation_channel"
```

## Naming drift

`k8s.restart_pods` overlaps semantically with the existing
`k8s.restart_deployment` (used by `runtime-restart-operator`). The
overlap is partial: `restart_deployment` always rolls the entire
Deployment; `restart_pods` adds a label-selector mode for partial
drains. The runtime team picks one of:

1. Register `k8s.restart_pods` as a new sibling tool with both modes.
   PIR's saturation-planner uses the new name unconditionally. (PIR's
   preference — keeps the two operators bound to non-overlapping tool
   profiles.)
2. Extend `k8s.restart_deployment` with the optional label-selector
   mode and have PIR use the existing name. Smaller surface but
   couples two specialists to one tool.

PIR is fine either way; option 1 is recommended because the policy
engine can keep `runtime-restart-operator` and `saturation-operator`
on disjoint capability lists, which is easier to audit.

## Governance invariants

The runtime's policy engine MUST enforce the following before any of
the four writes reaches the target system:

1. **Tool profile match.** Session metadata `tool_profile` must equal
   `saturation-operator-bounded` for the three K8s tools and
   `human-escalation-minimal` for `notify.escalation_channel`. Any
   other value (including missing) → `DENIED` with sentinel
   `tool_profile_mismatch`.
2. **Environment match.** Session metadata `environment` must equal
   the runtime's notion of the cluster's environment (label,
   runtime-config, or session value — see Open questions). Mismatch
   → `DENIED` with `environment_mismatch`.
3. **Replica + delta bounds.** `k8s.scale_deployment` rejects
   `replicas > 200` and `|replicas_delta| > 50` at the policy layer
   (the schema enforces it too; the policy gate is defense in depth).
4. **TTL bound on circuit-break.** `k8s.circuit_break.ttl_seconds`
   must be between 60 and 1800. Outside that range → `DENIED` with
   `ttl_out_of_bounds`. The runtime owns the auto-expire mechanism;
   PIR does not have to remember to call a release verb.
5. **Approval flag.** All four tools have `requires_approval: true`.
   PIR sets `InvokeToolRequest.approved=true` per invocation. Without
   it → `DENIED` with `approval_required`.
6. **Notify rate limit.** `notify.escalation_channel` is rate-limited
   per `incident_id` to one delivery per minute (transient
   re-deliveries from JetStream redelivery should not flood the
   channel). Excess → `DENIED` with `rate_limit_exceeded`. The
   runtime owns the rate-limit state since PIR sessions are
   short-lived.

Denials materialize as `Invocation{status: DENIED, error: {...}}` with
the sentinel above, matching the contract `runtime-rollout-operator`
already consumes.

## Acceptance criteria

- `DefaultCapabilities()` in `internal/adapters/tools/` registers the
  four tools with the shapes in §"Detailed capability specs".
- Unit tests in `internal/adapters/tools/` cover the happy path for
  each tool against fakes (k8s clientset and a mock notify provider).
- Policy engine enforces the six invariants in §"Governance
  invariants" with the named sentinel codes.
- The two tool profiles (`saturation-operator-bounded`,
  `human-escalation-minimal`) are documented somewhere PIR can
  reference (the value of `tool_profile` session metadata that the
  policy engine matches).
- `EventInvocationCompleted` / `EventInvocationDenied` events on
  `workspace.events.invocation.{completed,denied}` carry the
  `correlation_id` PIR passes in `InvokeToolRequest`.

## Open questions for the runtime team

1. **Mesh translation in `k8s.circuit_break`.** Does the runtime want
   to commit to one mesh CRD per cluster (auto-detected at startup)
   or accept a `mesh_kind` argument from PIR? PIR can supply
   `mesh_kind` via planner parameters if that's the cleaner shape.
2. **Notify provider routing.** PIR proposes the runtime owns the
   routing config (a map from `environment` to webhook /
   integration-key). Confirm the runtime is willing to hold this
   config and rotate credentials without PIR involvement.
3. **`k8s.restart_pods` vs `k8s.restart_deployment` naming.** Pick
   option 1 (sibling) or option 2 (extend) — see §"Naming drift".
4. **Rate limit storage.** Invariant 6 needs persisted state.
   Acceptable to keep it in process memory if the runtime is single-
   replica today, with a follow-up to move to a shared KV when the
   runtime scales out?
5. **Session approval flow.** Same question as the rollout proposal:
   per-invocation vs per-session. PIR's design is per-invocation.

## PIR-side readiness

These changes already landed (or are landing in this branch) and do
not block the proposal:

- **Specialist contract.** `saturation-operator` already lists
  `tool_profile: saturation-operator-deferred`; PIR will rename to
  `saturation-operator-bounded` once this proposal merges and add
  the three primary tools to the catalog. `human-escalation` already
  lists `tool_profile: human-escalation-minimal` and will gain
  `notify.escalation_channel` as its primary tool once registered.
- **Specialist executors.** `saturationoperator` and `humanescalation`
  packages exist and ship the kernel-first decision path; once tools
  are registered, each gets a follow-up slice that wires the runtime
  client and translates the LLM-decided action into an
  `InvokeToolRequest`. The decision graph + outcome event already
  materialise as expected without runtime invocation, which makes the
  follow-up purely additive.
- **Outcome event families.** Already present in
  `contracts/events/catalog.v1.yaml` for both pipelines:
  `payments.incident.resource-saturation.operation.{completed,failed,escalated}`
  and `payments.incident.{escalated.to-human,failed.terminal}`.

Everything on the PIR side can ship and be tested with fakes today;
cluster end-to-end of saturation + human-escalation actuation waits
on this proposal merging.
