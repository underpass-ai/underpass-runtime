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

## Security Considerations

UnderPass Runtime executes tools inside isolated workspaces on behalf of AI agents. Security is a core design concern:

- **Policy engine**: All tool invocations pass through a policy engine that validates scope, risk level, approval requirements, allowed paths, registries, namespaces, and more.
- **Workspace isolation**: Each session gets an isolated filesystem. Kubernetes backend uses separate pods with security contexts.
- **Auth modes**: Supports `payload` (no auth, for local dev) and `trusted_headers` (shared token, for production).
- **Container security**: Runs as non-root (distroless base image), drops all capabilities, read-only root filesystem.
- **Supply chain**: CI runs `govulncheck` on every push. SonarCloud scans for code quality and security issues.
