# Contributing to UnderPass Runtime

Thank you for your interest in contributing to UnderPass Runtime.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/<you>/underpass-runtime.git`
3. Create a feature branch: `git checkout -b feat/my-feature`
4. Make your changes
5. Run tests: `make test`
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

# Build container image
make docker-build
```

## Architecture

UnderPass Runtime follows hexagonal architecture:

- `internal/domain/` — Pure domain models (no dependencies)
- `internal/app/` — Business logic, interfaces (ports)
- `internal/adapters/` — Infrastructure implementations
- `internal/httpapi/` — HTTP transport layer
- `internal/bootstrap/` — Service initialization
- `cmd/workspace/` — Entry point

## Testing

- **Unit tests**: Hand-written fakes (no gomock/testify/mock). All tests run without real infrastructure.
- **Core coverage gate**: 80% minimum on `internal/app`, `internal/adapters/audit`, `internal/adapters/policy`, `internal/adapters/sessionstore`, `internal/adapters/invocationstore`.
- **E2E tests**: Python tests in `e2e/tests/`, deployed as K8s Jobs.

## Code Style

- Follow Go conventions and `golangci-lint` configuration (`.golangci.yml`)
- No stubs or placeholder implementations — implement real adapters
- API-first: check the HTTP API contract before modifying handlers
- Avoid over-engineering: only add what the change requires

## Pull Requests

- Keep PRs focused on a single change
- Include tests for new functionality
- Ensure `make test` and `make coverage-core` pass
- Write a clear PR description with context

## Reporting Issues

Use [GitHub Issues](https://github.com/underpass-ai/underpass-runtime/issues) to report bugs or request features.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
