package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthConfigFromEnv_DefaultPayload(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", "")
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "")

	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected default auth config error: %v", err)
	}
	if cfg.Mode != authModePayload {
		t.Fatalf("expected default payload mode, got %q", cfg.Mode)
	}
}

func TestAuthConfigFromEnv_TrustedHeadersRequiresToken(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", authModeTrustedHeaders)
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "")

	if _, err := AuthConfigFromEnv(); err == nil {
		t.Fatal("expected missing shared token error")
	}
}

func TestAuthConfigAuthenticatePrincipal(t *testing.T) {
	cfg := AuthConfig{
		Mode:         authModeTrustedHeaders,
		TenantHeader: "X-Workspace-Tenant-Id",
		ActorHeader:  "X-Workspace-Actor-Id",
		RolesHeader:  "X-Workspace-Roles",
		TokenHeader:  "X-Workspace-Auth-Token",
		SharedToken:  "shared-token",
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set("X-Workspace-Auth-Token", "shared-token")
	req.Header.Set("X-Workspace-Tenant-Id", "tenant-a")
	req.Header.Set("X-Workspace-Actor-Id", "actor-a")
	req.Header.Set("X-Workspace-Roles", "devops,developer,devops")

	principal, authErr := cfg.authenticatePrincipal(req)
	if authErr != nil {
		t.Fatalf("unexpected auth error: %#v", authErr)
	}
	if principal.TenantID != "tenant-a" || principal.ActorID != "actor-a" {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if len(principal.Roles) != 2 {
		t.Fatalf("expected deduped roles, got %#v", principal.Roles)
	}
}

func TestAuthConfigFromEnv_CustomHeaders(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", "payload")
	t.Setenv("WORKSPACE_AUTH_TENANT_HEADER", "X-Custom-Tenant")
	t.Setenv("WORKSPACE_AUTH_ACTOR_HEADER", "X-Custom-Actor")
	t.Setenv("WORKSPACE_AUTH_ROLES_HEADER", "X-Custom-Roles")
	t.Setenv("WORKSPACE_AUTH_TOKEN_HEADER", "X-Custom-Token")
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "tok")

	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TenantHeader != "X-Custom-Tenant" {
		t.Fatalf("expected custom tenant header, got %q", cfg.TenantHeader)
	}
	if cfg.SharedToken != "tok" {
		t.Fatalf("expected shared token, got %q", cfg.SharedToken)
	}
}

func TestAuthConfigFromEnv_TrustedHeadersValid(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", authModeTrustedHeaders)
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "my-secret")

	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatalf("expected trusted_headers with valid token to succeed, got %v", err)
	}
	if cfg.Mode != authModeTrustedHeaders {
		t.Fatalf("expected trusted_headers mode, got %q", cfg.Mode)
	}
}

func TestAuthConfigFromEnv_UnsupportedMode(t *testing.T) {
	t.Setenv("WORKSPACE_AUTH_MODE", "oauth2")
	t.Setenv("WORKSPACE_AUTH_SHARED_TOKEN", "")

	if _, err := AuthConfigFromEnv(); err == nil {
		t.Fatal("expected error for unsupported auth mode")
	}
}

func TestAuthConfigAuthenticatePrincipalMissingHeaders(t *testing.T) {
	cfg := AuthConfig{
		Mode:         authModeTrustedHeaders,
		TenantHeader: "X-Workspace-Tenant-Id",
		ActorHeader:  "X-Workspace-Actor-Id",
		RolesHeader:  "X-Workspace-Roles",
		TokenHeader:  "X-Workspace-Auth-Token",
		SharedToken:  "shared-token",
	}

	// Valid token but missing tenant/actor headers.
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set("X-Workspace-Auth-Token", "shared-token")

	_, authErr := cfg.authenticatePrincipal(req)
	if authErr == nil {
		t.Fatal("expected auth failure for missing principal headers")
	}
	if authErr.status != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status, got %d", authErr.status)
	}
}

func TestAuthConfigAuthenticatePrincipalRejectsInvalidToken(t *testing.T) {
	cfg := DefaultAuthConfig()
	cfg.Mode = authModeTrustedHeaders
	cfg.SharedToken = "expected-token"

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set(cfg.TokenHeader, "bad-token")
	req.Header.Set(cfg.TenantHeader, "tenant-a")
	req.Header.Set(cfg.ActorHeader, "actor-a")

	_, authErr := cfg.authenticatePrincipal(req)
	if authErr == nil {
		t.Fatal("expected auth failure")
	}
	if authErr.status != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %#v", authErr)
	}
}
