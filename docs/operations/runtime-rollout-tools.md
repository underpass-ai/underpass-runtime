# Runtime Rollout Tool Profile

`runtime-rollout-narrow` is the session `tool_profile` required to invoke the
specialist-scoped rollout tools:

- `k8s.get_replicasets`
- `k8s.rollout_pause`
- `k8s.rollout_undo`

## Session metadata

Create the session with:

- `tool_profile=runtime-rollout-narrow`
- `environment=<env>` for write tools

The runtime resolves its own environment from `session.metadata.runtime_environment`
when present, otherwise from `WORKSPACE_ENV`. `k8s.rollout_pause` and
`k8s.rollout_undo` are denied with `environment_mismatch` when the session
environment does not match the runtime environment.

## Rollback safeguards

`k8s.rollout_undo` performs rollback preflight checks before mutating the
Deployment:

- denies with `no_previous_healthy_replicaset` when no prior healthy ReplicaSet exists
- denies with `rollout_too_young` when the active ReplicaSet is younger than 60 seconds
- returns success with `rolled_back=false` when the target revision is already active
