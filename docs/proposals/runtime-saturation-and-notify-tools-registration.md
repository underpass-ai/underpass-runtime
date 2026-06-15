# Reference — saturation + escalation-notify tools in `underpass-runtime`

Status: ✅ registered on `main`. Tools are defined in `internal/adapters/tools/catalog_defaults.yaml` (`k8s.scale_deployment`, `k8s.restart_pods`, `k8s.circuit_break`, `notify.escalation_channel`), implemented in `k8s_saturation_tools.go` + `notify_tools.go`, enforced in `internal/adapters/policy/static_policy.go`, and covered by `e2e/tests/24-runtime-saturation-notify-tools`.

## Summary

The runtime registers four governed write tools that let a bounded
operator actuate two remediation scenarios through the runtime instead
of stopping at a decision:

1. **Resource saturation** — an operator converges on a plan whose
   `chosen_action` is one of `scale_up`, `restart_pods`,
   `circuit_break`, and needs the matching K8s write verb to actuate
   it.
2. **Human escalation** — an operator converges on `engage_owner` and
   needs a notification surface so the human owner sees the handoff
   without navigating to the kernel handoff node manually.

The runtime exposes **three K8s write tools** (`k8s.scale_deployment`,
`k8s.restart_pods`, `k8s.circuit_break`) and **one notification tool**
(`notify.escalation_channel`), and enforces the bounded invariants in
§"Governance invariants" at the policy layer. These tools are
purely write-side: callers read what they need from the kernel via
`GetContext`, not from the runtime.

## Current state in runtime

`grep -rn '"k8s\.' internal/adapters/tools/` enumerates the K8s
capabilities registered today:

```
k8s.apply_manifest
k8s.circuit_break
k8s.get_deployments
k8s.get_images
k8s.get_logs
k8s.get_pods
k8s.get_services
k8s.local
k8s.restart_deployment
k8s.restart_pods
k8s.rollout_status
k8s.scale_deployment
k8s.set_image
```

`notify.escalation_channel` is the first tool in the `notify.*`
namespace.

## Tool profiles

Two tool profiles bind these capabilities; the policy engine matches
them against the session-metadata `tool_profile` key:

- `saturation-operator-bounded` — binds the three K8s saturation tools.
- `human-escalation-minimal` — binds `notify.escalation_channel`.

## Detailed capability specs

All four tools share this common shape: scope `CLUSTER` for the
K8s tools and `EXTERNAL` for the notify tool, a `PolicyMetadata`
block requiring the matching `tool_profile` session-metadata key, and
`trace_name` / `span_name` matching the tool name.

### 1. `k8s.scale_deployment` (write)

Equivalent to `kubectl scale deployment/{name} -n {namespace}
--replicas={n}`. The tool accepts either an absolute `replicas` value
or a relative `replicas_delta`; exactly one must be present.

```yaml
name: k8s.scale_deployment
description: |
  Set Deployment.spec.replicas to an absolute target or apply a
  bounded delta. Idempotent on the absolute path (a no-op when the
  desired count already matches). The relative path is one-shot per
  invocation; the caller forwards the planner's parameters verbatim.
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

### 2. `k8s.restart_pods` (write)

Equivalent to `kubectl rollout restart deployment/{name} -n {namespace}`
when invoked at deployment scope, or to deleting pods matching a
label selector when invoked at label-selector scope. The plan names
exactly one mode.

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

### 3. `k8s.circuit_break` (write)

Apply a traffic-shed policy at the Service / VirtualService / Gateway
boundary. The exact CRD depends on the cluster's mesh choice; the
shape stays mesh-agnostic by carrying `target_service` +
`downstream` and asking the runtime to translate to whatever CRD the
cluster recognises (Istio `VirtualService`, Linkerd `ServiceProfile`,
or a NetworkPolicy fallback).

```yaml
name: k8s.circuit_break
description: |
  Install or update a traffic-shed policy that blocks traffic from a
  named upstream Service to a named downstream destination, applied
  at the cluster's mesh / network layer. The policy is applied with
  a TTL so it auto-removes if not renewed; the caller records the
  policy id in the operator's decision graph so the human owner can
  observe and remove it after recovery.
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

### 4. `notify.escalation_channel` (write — external)

Post a structured handoff to the configured human-escalation channel.
v1 targets a Slack incoming webhook; subsequent versions can fan out
to PagerDuty / Opsgenie / etc. The runtime owns the credential and
the channel routing; the caller passes only the handoff content.

```yaml
name: notify.escalation_channel
description: |
  Notify the configured human-escalation channel with a structured
  handoff message. The runtime resolves channel / webhook by the
  session's environment + a runtime-side routing config; callers
  never ship credentials or URLs. The notification carries a stable
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
drains. The runtime registers `k8s.restart_pods` as a new sibling
tool with both modes, which keeps the two operators bound to
non-overlapping tool profiles and the policy engine able to keep
`runtime-restart-operator` and the saturation operator on disjoint
capability lists (easier to audit).

## Governance invariants

The runtime's policy engine enforces the following before any of the
four writes reaches the target system:

1. **Tool profile match.** Session metadata `tool_profile` must equal
   `saturation-operator-bounded` for the three K8s tools and
   `human-escalation-minimal` for `notify.escalation_channel`. Any
   other value (including missing) → `DENIED` with sentinel
   `tool_profile_mismatch`.
2. **Environment match.** Session metadata `environment` must equal
   the runtime's notion of the cluster's environment. Mismatch
   → `DENIED` with `environment_mismatch`.
3. **Replica + delta bounds.** `k8s.scale_deployment` rejects
   `replicas > 200` and `|replicas_delta| > 50` at the policy layer
   (the schema enforces it too; the policy gate is defense in depth).
4. **TTL bound on circuit-break.** `k8s.circuit_break.ttl_seconds`
   must be between 60 and 1800. Outside that range → `DENIED` with
   `ttl_out_of_bounds`. The runtime owns the auto-expire mechanism, so
   callers do not have to remember to call a release verb.
5. **Approval flag.** All four tools have `requires_approval: true`.
   The caller sets `InvokeToolRequest.approved=true` per invocation.
   Without it → `DENIED` with `approval_required`.
6. **Notify rate limit.** `notify.escalation_channel` is rate-limited
   per `incident_id` to one delivery per minute (transient
   re-deliveries from JetStream redelivery should not flood the
   channel). Excess → `DENIED` with `rate_limit_exceeded`. The
   runtime owns the rate-limit state since operator sessions are
   short-lived.

Denials materialize as `Invocation{status: DENIED, error: {...}}` with
the sentinel above.
