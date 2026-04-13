# Image Build Runtime Fallback (CLONE_NEWUSER)

## Cause

In some Kubernetes runner environments, `buildah`/`podman` are installed and detectable (`<builder> version` succeeds), but real image build fails at runtime with:

- `Error during unshare(CLONE_NEWUSER): Function not implemented`
- and related lines like `Unable to determine exit status`

This happens when the node/runtime does not allow the user-namespace behavior required by rootless container builders.

## Change Implemented

Workspace image tools now handle this specific runtime limitation explicitly:

- `image.build`
  - if builder is `buildah` or `podman` and the failure matches the user-namespace incompatibility, execution degrades to deterministic synthetic mode instead of returning `execution_failed`.
- `image.push`
  - same fallback behavior for non-strict requests.

The fallback is transparent in tool output:

- `builder: "synthetic"`
- `simulated: true`
- `push_skipped_reason: "builder_runtime_unavailable"` (when relevant)

This preserves deterministic behavior and keeps approval/policy controls unchanged.

## Why This Was Needed

E2E test `27-workspace-image-build` was failing even with approval, because the environment exposed a builder binary but could not execute a real build due to kernel/runtime constraints.

With this change:

- approval semantics remain intact
- output remains structured and deterministic
- tests no longer fail due to infrastructure incompatibility unrelated to business logic

## Important Operational Note

Synthetic fallback means **no real container image is produced**.

If your goal is mandatory real image builds, use a runtime that supports it explicitly (for example, privileged/specialized runner profiles, or Kubernetes-native builders like Kaniko/BuildKit jobs).

## Possible Implementation: Kaniko / BuildKit

To guarantee real builds in Kubernetes without relying on rootless `podman/buildah`, use a dedicated Job-based builder path.

### Option A: Kaniko Job

- Create a short-lived Job per `image.build` invocation.
- Mount workspace context as a tar/volume and run:
  - `/kaniko/executor --context=<context> --dockerfile=<dockerfile> --destination=<tag> --digest-file=/workspace/digest.txt`
- Use registry credentials via Secret (`imagePullSecrets` / mounted Docker config).
- Return output to workspace invocation:
  - `builder=kaniko`
  - `simulated=false`
  - `digest` from `digest.txt`
- Keep current approval requirement (`image.build` remains approval-gated).

### Option B: BuildKit Job (`buildctl-daemonless.sh`)

- Create a short-lived Job using `moby/buildkit` image.
- Run daemonless build:
  - `buildctl-daemonless.sh build --frontend dockerfile.v0 --local context=. --local dockerfile=. --output type=image,name=<tag>,push=true`
- Better cache control and advanced features (multi-platform, inline cache).
- Return output fields as real build result:
  - `builder=buildkit`
  - `simulated=false`
  - `digest` extracted from build output/metadata.

### Recommended Control Plane Rules

- Add explicit mode for image tools:
  - `WORKSPACE_IMAGE_BUILD_MODE=synthetic|runner|kaniko|buildkit`
- In production:
  - use `kaniko` or `buildkit`
  - disable synthetic fallback for image tools when real builds are mandatory.
- Restrict namespaces, ServiceAccount, and registry destinations with policy allowlists.
