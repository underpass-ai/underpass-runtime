# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in UnderPass Runtime, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@underpass.ai** with:

1. A description of the vulnerability
2. Steps to reproduce
3. Potential impact
4. Suggested fix (if any)

We will acknowledge receipt within 48 hours and aim to provide a fix within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

## Security Architecture

For the full security model including trust boundaries, threat model, and
authorization layers, see **[docs/architecture/security-model.md](docs/architecture/security-model.md)**.

### Key Security Controls

- **Policy engine**: All tool invocations pass through a policy engine that validates scope, risk level, approval requirements, allowed paths, registries, namespaces, and rate limits.
- **Workspace isolation**: Each session gets an isolated filesystem. Kubernetes backend uses separate pods with security contexts (non-root, drop ALL capabilities, read-only root filesystem).
- **Auth modes**: `payload` (no auth, for local dev) and `trusted_headers` (shared token with constant-time comparison, for production). Mutual TLS available for zero-trust environments.
- **TLS 1.3 minimum**: Enforced on all transports (HTTP server, Valkey, NATS, S3, OTLP). See [docs/operations/deployment-tls.md](docs/deployment-tls.md).
- **Container security**: Runs as non-root (UID 65532) on distroless base image. All Linux capabilities dropped. Seccomp RuntimeDefault profile.
- **Audit logging**: All invocations recorded with sensitive data redaction (tokens, passwords, API keys, bearer tokens, URL credentials).
- **Supply chain**: CI runs `govulncheck` and CodeQL on every push. SonarCloud enforces quality gates (70% overall coverage, 80% new code).

### Trust Boundaries

| Boundary | Protection |
|---|---|
| Caller → Runtime | HTTPS (TLS 1.3), shared token or mTLS |
| Runtime → Valkey | TLS + password AUTH, optional mTLS |
| Runtime → NATS | TLS, optional mTLS |
| Runtime → S3/MinIO | HTTPS + IAM credentials |
| Runtime → OTLP | TLS (configurable) |

See [docs/architecture/security-model.md](docs/architecture/security-model.md) for the full threat model
with 10 threat scenarios and known gaps.
