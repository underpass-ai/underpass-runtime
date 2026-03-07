package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	testHeaderTenantID   = "X-Workspace-Tenant-Id"
	testHeaderActorID    = "X-Workspace-Actor-Id"
	testHeaderAuthToken  = "X-Workspace-Auth-Token"
	testHeaderRoles      = "X-Workspace-Roles"
	testSharedToken      = "shared-token"
	testTenantA          = "tenant-a"
	testActorA           = "actor-a"
	testEnvAuthMode      = "WORKSPACE_AUTH_MODE"
	testEnvSharedToken   = "WORKSPACE_AUTH_SHARED_TOKEN"
	testSessionsEndpoint = "/v1/sessions"
)

func TestAuthConfigFromEnv_DefaultPayload(t *testing.T) {
	t.Setenv(testEnvAuthMode, "")
	t.Setenv(testEnvSharedToken, "")

	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected default auth config error: %v", err)
	}
	if cfg.Mode != authModePayload {
		t.Fatalf("expected default payload mode, got %q", cfg.Mode)
	}
}

func TestAuthConfigFromEnv_TrustedHeadersRequiresToken(t *testing.T) {
	t.Setenv(testEnvAuthMode, authModeTrustedHeaders)
	t.Setenv(testEnvSharedToken, "")

	if _, err := AuthConfigFromEnv(); err == nil {
		t.Fatal("expected missing shared token error")
	}
}

func TestAuthConfigAuthenticatePrincipal(t *testing.T) {
	cfg := AuthConfig{
		Mode:         authModeTrustedHeaders,
		TenantHeader: testHeaderTenantID,
		ActorHeader:  testHeaderActorID,
		RolesHeader:  testHeaderRoles,
		TokenHeader:  testHeaderAuthToken,
		SharedToken:  testSharedToken,
	}

	req := httptest.NewRequest(http.MethodPost, testSessionsEndpoint, nil)
	req.Header.Set(testHeaderAuthToken, testSharedToken)
	req.Header.Set(testHeaderTenantID, testTenantA)
	req.Header.Set(testHeaderActorID, testActorA)
	req.Header.Set(testHeaderRoles, "devops,developer,devops")

	principal, authErr := cfg.authenticatePrincipal(req)
	if authErr != nil {
		t.Fatalf("unexpected auth error: %#v", authErr)
	}
	if principal.TenantID != testTenantA || principal.ActorID != testActorA {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if len(principal.Roles) != 2 {
		t.Fatalf("expected deduped roles, got %#v", principal.Roles)
	}
}

func TestAuthConfigFromEnv_CustomHeaders(t *testing.T) {
	t.Setenv(testEnvAuthMode, "payload")
	t.Setenv("WORKSPACE_AUTH_TENANT_HEADER", "X-Custom-Tenant")
	t.Setenv("WORKSPACE_AUTH_ACTOR_HEADER", "X-Custom-Actor")
	t.Setenv("WORKSPACE_AUTH_ROLES_HEADER", "X-Custom-Roles")
	t.Setenv("WORKSPACE_AUTH_TOKEN_HEADER", "X-Custom-Token")
	t.Setenv(testEnvSharedToken, "tok")

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
	t.Setenv(testEnvAuthMode, authModeTrustedHeaders)
	t.Setenv(testEnvSharedToken, "my-secret")

	cfg, err := AuthConfigFromEnv()
	if err != nil {
		t.Fatalf("expected trusted_headers with valid token to succeed, got %v", err)
	}
	if cfg.Mode != authModeTrustedHeaders {
		t.Fatalf("expected trusted_headers mode, got %q", cfg.Mode)
	}
}

func TestAuthConfigFromEnv_UnsupportedMode(t *testing.T) {
	t.Setenv(testEnvAuthMode, "oauth2")
	t.Setenv(testEnvSharedToken, "")

	if _, err := AuthConfigFromEnv(); err == nil {
		t.Fatal("expected error for unsupported auth mode")
	}
}

func TestAuthConfigAuthenticatePrincipalMissingHeaders(t *testing.T) {
	cfg := AuthConfig{
		Mode:         authModeTrustedHeaders,
		TenantHeader: testHeaderTenantID,
		ActorHeader:  testHeaderActorID,
		RolesHeader:  testHeaderRoles,
		TokenHeader:  testHeaderAuthToken,
		SharedToken:  testSharedToken,
	}

	// Valid token but missing tenant/actor headers.
	req := httptest.NewRequest(http.MethodPost, testSessionsEndpoint, nil)
	req.Header.Set(testHeaderAuthToken, testSharedToken)

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

	req := httptest.NewRequest(http.MethodPost, testSessionsEndpoint, nil)
	req.Header.Set(cfg.TokenHeader, "bad-token")
	req.Header.Set(cfg.TenantHeader, testTenantA)
	req.Header.Set(cfg.ActorHeader, testActorA)

	_, authErr := cfg.authenticatePrincipal(req)
	if authErr == nil {
		t.Fatal("expected auth failure")
	}
	if authErr.status != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %#v", authErr)
	}
}
