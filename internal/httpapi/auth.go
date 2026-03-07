package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	authModePayload        = "payload"
	authModeTrustedHeaders = "trusted_headers"
)

type AuthConfig struct {
	Mode         string
	TenantHeader string
	ActorHeader  string
	RolesHeader  string
	TokenHeader  string
	SharedToken  string
}

type authFailure struct {
	status  int
	code    string
	message string
}

func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		Mode:         authModePayload,
		TenantHeader: "X-Workspace-Tenant-Id",
		ActorHeader:  "X-Workspace-Actor-Id",
		RolesHeader:  "X-Workspace-Roles",
		TokenHeader:  "X-Workspace-Auth-Token",
		SharedToken:  "",
	}
}

func AuthConfigFromEnv() (AuthConfig, error) {
	cfg := DefaultAuthConfig()

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_MODE")))
	if mode != "" {
		cfg.Mode = mode
	}

	if tenantHeader := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_TENANT_HEADER")); tenantHeader != "" {
		cfg.TenantHeader = tenantHeader
	}
	if actorHeader := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_ACTOR_HEADER")); actorHeader != "" {
		cfg.ActorHeader = actorHeader
	}
	if rolesHeader := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_ROLES_HEADER")); rolesHeader != "" {
		cfg.RolesHeader = rolesHeader
	}
	if tokenHeader := strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_TOKEN_HEADER")); tokenHeader != "" {
		cfg.TokenHeader = tokenHeader
	}
	cfg.SharedToken = strings.TrimSpace(os.Getenv("WORKSPACE_AUTH_SHARED_TOKEN"))

	switch cfg.Mode {
	case authModePayload:
		return cfg, nil
	case authModeTrustedHeaders:
		if cfg.TenantHeader == "" || cfg.ActorHeader == "" || cfg.TokenHeader == "" {
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

func (c AuthConfig) requiresAuthenticatedPrincipal() bool {
	return strings.EqualFold(strings.TrimSpace(c.Mode), authModeTrustedHeaders)
}

func (c AuthConfig) authenticatePrincipal(r *http.Request) (domain.Principal, *authFailure) {
	if !c.requiresAuthenticatedPrincipal() {
		return domain.Principal{}, nil
	}

	token := strings.TrimSpace(r.Header.Get(c.TokenHeader))
	if subtle.ConstantTimeCompare([]byte(token), []byte(c.SharedToken)) != 1 {
		return domain.Principal{}, &authFailure{
			status:  http.StatusUnauthorized,
			code:    "unauthorized",
			message: "invalid authentication token",
		}
	}

	tenantID := strings.TrimSpace(r.Header.Get(c.TenantHeader))
	actorID := strings.TrimSpace(r.Header.Get(c.ActorHeader))
	if tenantID == "" || actorID == "" {
		return domain.Principal{}, &authFailure{
			status:  http.StatusUnauthorized,
			code:    "unauthorized",
			message: "authenticated principal headers are required",
		}
	}

	return domain.Principal{
		TenantID: tenantID,
		ActorID:  actorID,
		Roles:    parseRolesHeader(r.Header.Get(c.RolesHeader)),
	}, nil
}

func parseRolesHeader(raw string) []string {
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
