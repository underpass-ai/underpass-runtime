# Workspace Runner Images

This document defines production runner images used by Kubernetes workspace sessions.

## Profiles

- `base`
  - Minimal SWE runtime tools: `git`, `patch`, `ripgrep`, shell utilities.
- `toolchains`
  - Multi-language build/test tooling: Go, Python, Node.js, Rust, C/C++ build stack.
- `secops`
  - Security tooling: `trivy`, `syft`.
- `container`
  - Container CLIs for constrained runtime operations: `podman`, `buildah`, `skopeo`.
- `k6`
  - API benchmark runtime: `k6`.
- `fat`
  - Union profile with toolchains + secops + k6 + container CLIs.

## Build

```bash
make runner-build PROFILE=all TAG=v0.1.0
```

Build a single profile:

```bash
make runner-build PROFILE=toolchains TAG=v0.1.0
```

## Push

```bash
make runner-push PROFILE=all TAG=v0.1.0
```

Or build and push in one step:

```bash
make runner-build-push PROFILE=all TAG=v0.1.0
```

## Registry naming

Image names follow this pattern:

- `registry.underpassai.com/swe-ai-fleet/workspace-runner-base:<tag>`
- `registry.underpassai.com/swe-ai-fleet/workspace-runner-toolchains:<tag>`
- `registry.underpassai.com/swe-ai-fleet/workspace-runner-secops:<tag>`
- `registry.underpassai.com/swe-ai-fleet/workspace-runner-container:<tag>`
- `registry.underpassai.com/swe-ai-fleet/workspace-runner-k6:<tag>`
- `registry.underpassai.com/swe-ai-fleet/workspace-runner-fat:<tag>`

## Wiring

`deploy/k8s/30-microservices/workspace.yaml` maps runner profiles through:

- `WORKSPACE_K8S_RUNNER_IMAGE`
- `WORKSPACE_K8S_RUNNER_IMAGE_BUNDLES_JSON`

Unknown `runner_profile` values are rejected by workspace session creation.
