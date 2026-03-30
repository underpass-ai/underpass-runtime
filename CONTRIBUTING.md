# Contributing to UnderPass Runtime

Thank you for your interest in contributing to UnderPass Runtime.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/<you>/underpass-runtime.git`
3. Create a feature branch: `git checkout -b feat/my-feature`
4. Make your changes
5. Run quality gate: `bash scripts/ci/quality-gate.sh`
6. Push and open a pull request

## Development Setup

```bash
# Prerequisites: Go 1.25+, Docker or Podman

# Run locally
go run ./cmd/workspace

# Run with Valkey (session persistence)
docker compose up

# Run tests
make test

# Coverage gate (core packages, 80% minimum)
make coverage-core

# Full coverage report
make coverage-full

# Local quality gate (mirrors CI)
bash scripts/ci/quality-gate.sh

# Quick mode (skips coverage gate)
bash scripts/ci/quality-gate.sh --quick

# Build container image
make docker-build
```

## Architecture

UnderPass Runtime follows hexagonal architecture. See
[ADR-001](docs/adr/ADR-001-hexagonal-architecture-in-go.md) for the full
rationale.

- `internal/domain/` — Pure domain models (no dependencies)
- `internal/app/` — Business logic, port interfaces (`types.go`)
- `internal/adapters/` — Infrastructure implementations (tools, workspace, policy, stores, eventbus, telemetry)
- `internal/httpapi/` — HTTP transport layer
- `internal/bootstrap/` — Composition root (wires ports to adapters)
- `cmd/workspace/` — Entry point

## Documentation

- **[docs/README.md](docs/README.md)** — Documentation index
- **[docs/security-model.md](docs/security-model.md)** — Trust boundaries and threat model
- **[docs/testing.md](docs/testing.md)** — Test matrix, coverage gates, E2E tiers
- **[docs/adr/](docs/adr/)** — Architecture Decision Records
- **[docs/operations/](docs/operations/)** — Deployment and cluster prerequisites
- **[docs/runbooks/](docs/runbooks/)** — Incident response, scaling, TLS rotation

When making architectural decisions, add an ADR in `docs/adr/` following the
existing format (Context, Decision, Consequences, Alternatives Considered).

## Testing

- **Unit tests**: Hand-written fakes (no gomock/testify/mock). All tests run without real infrastructure.
- **Core coverage gate**: 80% minimum on `internal/app`, `internal/adapters/audit`, `internal/adapters/policy`, `internal/adapters/sessionstore`, `internal/adapters/invocationstore`.
- **E2E tests**: Python tests in `e2e/tests/`, deployed as K8s Jobs. See [docs/testing.md](docs/testing.md).

See [docs/testing.md](docs/testing.md) for the full test matrix and how to
run tests at each level.

## Code Style

- Follow Go conventions and `golangci-lint` configuration (`.golangci.yml`)
- No stubs or placeholder implementations — implement real adapters
- API-first: check the HTTP API contract before modifying handlers
- Avoid over-engineering: only add what the change requires
- Add doc comments to exported constructors and public interfaces

## Pull Requests

- Keep PRs focused on a single change
- Include tests for new functionality
- Ensure `bash scripts/ci/quality-gate.sh` passes (or at minimum `make test && make coverage-core`)
- Write a clear PR description with context

## Security

See [SECURITY.md](SECURITY.md) for vulnerability reporting and
[docs/security-model.md](docs/security-model.md) for the threat model.

## Reporting Issues

Use [GitHub Issues](https://github.com/underpass-ai/underpass-runtime/issues) to report bugs or request features.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
