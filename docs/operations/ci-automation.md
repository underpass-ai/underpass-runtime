# CI Automation

## Image builds

All container images are built and pushed to `ghcr.io/underpass-ai/underpass-runtime`
automatically by CI.

CI prefers the repository secrets `GHCR_USERNAME` and `GHCR_TOKEN` for registry
authentication. If they are absent, workflows fall back to `GITHUB_TOKEN`.

### On merge to main

The `CI — Workspace` workflow builds and pushes:

| Image | Dockerfile | Tags |
|-------|-----------|------|
| `underpass-runtime` | `./Dockerfile` | `{appVersion}`, `{sha7}`, `grpc-latest` |
| `e2e-runner` | `e2e/Dockerfile` | `{appVersion}`, `e2e-latest` |
| `cert-gen` | `images/cert-gen/Dockerfile` | `{appVersion}`, `v1.0.0` |

The `CI — Tool Learning` workflow builds and pushes:

| Image | Dockerfile | Tags |
|-------|-----------|------|
| `tool-learning` | `services/tool-learning/Dockerfile` | `{appVersion}`, `latest` |

### On tag release (v*)

The `Release` workflow builds and pushes all 4 images with the release
version tag, plus creates a GitHub Release with binaries.

### Version alignment

All images share the version from `charts/underpass-runtime/Chart.yaml`
`appVersion` field. The Helm chart templates default to this version:

```yaml
image:
  tag: ""  # defaults to Chart.appVersion
e2e:
  image:
    tag: ""  # defaults to Chart.appVersion
toolLearning:
  image:
    tag: ""  # defaults to Chart.appVersion
```

This means `helm install` with no tag override always pulls the version
that matches the chart.

## CI workflows

| Workflow | Trigger | What it does |
|----------|---------|-------------|
| `ci.yml` | Push to main, PRs (non-services/) | Lint, build, test (2 variants), CodeQL, Docker build+push, SonarCloud |
| `ci-tool-learning.yml` | Push to main, PRs (services/tool-learning/) | Decoupling guard, lint, build, test, CodeQL, Docker build+push, SonarCloud |
| `release.yml` | Tag `v*` | Build binaries, GitHub Release, build+push all 4 images |

## Quality gates

| Gate | Threshold | Scope |
|------|-----------|-------|
| Core coverage | >= 80% | `internal/app`, `internal/adapters/{audit,policy,sessionstore,invocationstore}` |
| Full coverage | >= 70% | All packages (SonarCloud) |
| New code coverage | >= 80% | Changed lines in PR (SonarCloud) |
| Duplication | <= 3% | New code (SonarCloud) |
| Security hotspots | 100% reviewed | New code (SonarCloud) |
| govulncheck | Advisory | All dependencies |
| CodeQL | Blocking | Go static analysis |
| golangci-lint | Blocking | Configured linters |

## No manual builds required

After merging to main, all images are available in the registry. To deploy:

```bash
helm upgrade underpass-runtime charts/underpass-runtime \
  --set image.pullPolicy=Always
```

The `Always` pull policy ensures the latest image is pulled. For
production, pin to a specific appVersion tag instead.

## Registry auth

If your organization-level GHCR package does not grant write access to the
repository `GITHUB_TOKEN`, set these repository secrets:

| Secret | Purpose |
|--------|---------|
| `GHCR_USERNAME` | Username that owns the package-push token |
| `GHCR_TOKEN` | Token with `write:packages` for `ghcr.io` |
