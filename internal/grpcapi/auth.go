package grpcapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"os"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	authModePayload        = "payload"
	authModeTrustedHeaders = "trusted_headers"
)

// AuthConfig configures gRPC authentication. Mirrors the HTTP auth config
// but reads from gRPC metadata instead of HTTP headers.
type AuthConfig struct {
	Mode         string // "payload" or "trusted_headers"
	TenantKey    string // metadata key for tenant ID
	ActorKey     string // metadata key for actor ID
	RolesKey     string // metadata key for roles (comma-separated)
	TokenKey     string // metadata key for auth token
	SharedToken  string // expected token value
}

// DefaultAuthConfig returns auth config for development (no auth required).
func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		Mode:      authModePayload,
		TenantKey: "x-workspace-tenant-id",
		ActorKey:  "x-workspace-actor-id",
		RolesKey:  "x-workspace-roles",
		TokenKey:  "x-workspace-auth-token",
	}
}

// AuthConfigFromEnv loads auth config from environment variables,
// mirroring the behavior of the HTTP auth config.
func AuthConfigFromEnv() (AuthConfig, error) {
	cfg := DefaultAuthConfig()

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_MODE")))
	if mode != "" {
		cfg.Mode = mode
	}
	if v := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_TENANT_HEADER")); v != "" {
		cfg.TenantKey = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_ACTOR_HEADER")); v != "" {
		cfg.ActorKey = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_ROLES_HEADER")); v != "" {
		cfg.RolesKey = strings.ToLower(v)
	}
	if v := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_TOKEN_HEADER")); v != "" {
		cfg.TokenKey = strings.ToLower(v)
	}
	cfg.SharedToken = strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_SHARED_TOKEN"))

	switch cfg.Mode {
	case authModePayload:
		return cfg, nil
	case authModeTrustedHeaders:
		if cfg.TenantKey == "" || cfg.ActorKey == "" || cfg.TokenKey == "" {
			return AuthConfig{}, fmt.Errorf("trusted_headers mode requires tenant/actor/token header names")
		}
		if cfg.SharedToken == "" {
			return AuthConfig{}, fmt.Errorf("trusted_headers mode requires WORKSPACE_AUTH_SHARED_TOKEN")
		}
		return cfg, nil
	default:
		return AuthConfig{}, fmt.Errorf("unsupported WORKSPACE_AUTH_MODE: %s", cfg.Mode)
	}
}

type principalKey struct{}

// PrincipalFromContext extracts the authenticated principal from context.
func PrincipalFromContext(ctx context.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(domain.Principal)
	return p, ok
}

// AuthEnabled returns true if the config requires authentication.
func (c AuthConfig) AuthEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.Mode), authModeTrustedHeaders)
}

// UnaryAuthInterceptor returns a gRPC unary interceptor that extracts
// and validates the principal from metadata when auth is enabled.
func UnaryAuthInterceptor(cfg AuthConfig) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if !cfg.AuthEnabled() {
			return handler(ctx, req)
		}
		principal, err := authenticateFromMetadata(ctx, cfg)
		if err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, principalKey{}, principal)
		return handler(ctx, req)
	}
}

func authenticateFromMetadata(ctx context.Context, cfg AuthConfig) (domain.Principal, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return domain.Principal{}, status.Error(codes.Unauthenticated, "missing metadata")
	}

	token := firstValue(md, cfg.TokenKey)
	if subtle.ConstantTimeCompare([]byte(token), []byte(cfg.SharedToken)) != 1 {
		return domain.Principal{}, status.Error(codes.Unauthenticated, "invalid authentication token")
	}

	tenantID := firstValue(md, cfg.TenantKey)
	actorID := firstValue(md, cfg.ActorKey)
	if tenantID == "" || actorID == "" {
		return domain.Principal{}, status.Error(codes.Unauthenticated, "tenant_id and actor_id are required")
	}

	return domain.Principal{
		TenantID: tenantID,
		ActorID:  actorID,
		Roles:    parseRoles(firstValue(md, cfg.RolesKey)),
	}, nil
}

func firstValue(md metadata.MD, key string) string {
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

func parseRoles(raw string) []string {
	entries := strings.Split(raw, ",")
	roles := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		role := strings.TrimSpace(entry)
		if role == "" {
			continue
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	return roles
}
