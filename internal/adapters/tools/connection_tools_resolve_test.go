package tools

import (
	"os"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

// ---------------------------------------------------------------------------
// resolveTypedProfile
// ---------------------------------------------------------------------------

func TestResolveTypedProfile_EmptyID(t *testing.T) {
	session := domain.Session{AllowedPaths: []string{"."}}
	_, _, err := resolveTypedProfile(session, "", []string{"nats"}, "dev.nats", "nats://localhost:4222")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty ID, got %#v", err)
	}
}

func TestResolveTypedProfile_WhitespaceID(t *testing.T) {
	session := domain.Session{AllowedPaths: []string{"."}}
	_, _, err := resolveTypedProfile(session, "   ", []string{"nats"}, "dev.nats", "nats://localhost:4222")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for whitespace-only ID, got %#v", err)
	}
}

func TestResolveTypedProfile_NotFound(t *testing.T) {
	session := domain.Session{AllowedPaths: []string{"."}}
	_, _, err := resolveTypedProfile(session, "nonexistent.profile", []string{"nats"}, "", "")
	if err == nil || err.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not_found for unknown profile, got %#v", err)
	}
}

func TestResolveTypedProfile_KindMismatch(t *testing.T) {
	session := domain.Session{AllowedPaths: []string{"."}}
	// dev.nats exists in defaults with kind "nats"; ask for kind "redis" → mismatch
	_, _, err := resolveTypedProfile(session, "dev.nats", []string{"redis"}, "", "")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for kind mismatch, got %#v", err)
	}
	if err.Message != "profile kind does not match expected type" {
		t.Fatalf("unexpected error message: %q", err.Message)
	}
}

func TestResolveTypedProfile_EndpointNotConfigured(t *testing.T) {
	// Ensure env var is empty so resolveProfileEndpoint returns ""
	os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	session := domain.Session{AllowedPaths: []string{"."}}
	// dev.nats exists, kind matches, but no env endpoint and profileID != defaultID → empty endpoint
	_, _, err := resolveTypedProfile(session, "dev.nats", []string{"nats"}, "other.nats", "nats://fallback:4222")
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed for unconfigured endpoint, got %#v", err)
	}
}

func TestResolveTypedProfile_DefaultEndpointFallback(t *testing.T) {
	os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	session := domain.Session{AllowedPaths: []string{"."}}
	profile, endpoint, err := resolveTypedProfile(session, "dev.nats", []string{"nats"}, "dev.nats", "nats://fallback:4222")
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	if endpoint != "nats://fallback:4222" {
		t.Fatalf("expected fallback endpoint, got %q", endpoint)
	}
	if profile.ID != "dev.nats" {
		t.Fatalf("expected profile ID dev.nats, got %q", profile.ID)
	}
}

func TestResolveTypedProfile_EnvEndpointUsed(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{"dev.nats":"nats://custom:4222"}`)
	os.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{"dev.nats":["custom"]}`)
	defer func() {
		os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
		os.Unsetenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON")
	}()

	session := domain.Session{AllowedPaths: []string{"."}}
	_, endpoint, err := resolveTypedProfile(session, "dev.nats", []string{"nats"}, "dev.nats", "nats://fallback:4222")
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	if endpoint != "nats://custom:4222" {
		t.Fatalf("expected env endpoint, got %q", endpoint)
	}
}

func TestResolveTypedProfile_MultipleAllowedKinds(t *testing.T) {
	os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	session := domain.Session{AllowedPaths: []string{"."}}
	// dev.mongo exists with kind "mongo"; allow both "mongo" and "mongodb"
	profile, endpoint, err := resolveTypedProfile(session, "dev.mongo", []string{"mongo", "mongodb"}, "dev.mongo", "mongodb://fallback:27017")
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	if profile.Kind != "mongo" {
		t.Fatalf("expected kind mongo, got %q", profile.Kind)
	}
	if endpoint != "mongodb://fallback:27017" {
		t.Fatalf("expected fallback endpoint, got %q", endpoint)
	}
}

func TestResolveTypedProfile_FilteredByAllowlist(t *testing.T) {
	os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.redis",
		},
	}
	// dev.nats exists but is filtered out by allowlist
	_, _, err := resolveTypedProfile(session, "dev.nats", []string{"nats"}, "dev.nats", "nats://fallback:4222")
	if err == nil || err.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not_found when profile filtered by allowlist, got %#v", err)
	}
}

// ---------------------------------------------------------------------------
// clampInt
// ---------------------------------------------------------------------------

func TestClampInt(t *testing.T) {
	tests := []struct {
		name     string
		value    int
		min      int
		max      int
		fallback int
		want     int
	}{
		{"zero uses fallback", 0, 1, 100, 50, 50},
		{"below min clamps to min", 0, 10, 100, 5, 10},
		{"above max clamps to max", 200, 1, 100, 50, 100},
		{"in range returns value", 42, 1, 100, 50, 42},
		{"exact min", 1, 1, 100, 50, 1},
		{"exact max", 100, 1, 100, 50, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampInt(tt.value, tt.min, tt.max, tt.fallback)
			if got != tt.want {
				t.Fatalf("clampInt(%d, %d, %d, %d) = %d, want %d", tt.value, tt.min, tt.max, tt.fallback, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// endpointHost
// ---------------------------------------------------------------------------

func TestEndpointHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"nats://my-host:4222", "my-host"},
		{"mongodb://MONGO.local:27017", "mongo.local"},
		{"redis.local:6379", "redis.local"},
		{"amqp://guest:pass@rabbit.local:5672/", "rabbit.local"},
		{"https://Example.COM/path", "example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := endpointHost(tt.input)
			if got != tt.want {
				t.Fatalf("endpointHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hostMatchesAllowRule
// ---------------------------------------------------------------------------

func TestHostMatchesAllowRule(t *testing.T) {
	tests := []struct {
		host string
		rule string
		want bool
	}{
		{"", "example.com", false},
		{"example.com", "", false},
		{"example.com", "example.com", true},
		{"other.com", "example.com", false},
		{"sub.example.com", "*.example.com", true},
		{"example.com", "*.example.com", true},
		{"notexample.com", "*.example.com", false},
		{"10.0.0.5", "10.0.0.0/24", true},
		{"10.0.1.5", "10.0.0.0/24", false},
		{"not-an-ip", "10.0.0.0/24", false},
	}
	for _, tt := range tests {
		t.Run(tt.host+"_"+tt.rule, func(t *testing.T) {
			got := hostMatchesAllowRule(tt.host, tt.rule)
			if got != tt.want {
				t.Fatalf("hostMatchesAllowRule(%q, %q) = %v, want %v", tt.host, tt.rule, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveProfileEndpoint / profileEndpointAllowed
// ---------------------------------------------------------------------------

func TestResolveProfileEndpoint_NoEnvVar(t *testing.T) {
	os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	result := resolveProfileEndpoint(nil, "dev.nats")
	if result != "" {
		t.Fatalf("expected empty endpoint without env var, got %q", result)
	}
}

func TestResolveProfileEndpoint_InvalidJSON(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{invalid}`)
	defer os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	result := resolveProfileEndpoint(nil, "dev.nats")
	if result != "" {
		t.Fatalf("expected empty endpoint for invalid JSON, got %q", result)
	}
}

func TestResolveProfileEndpoint_MissingProfile(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{"dev.redis":"redis://localhost:6379"}`)
	defer os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
	result := resolveProfileEndpoint(nil, "dev.nats")
	if result != "" {
		t.Fatalf("expected empty endpoint for missing profile, got %q", result)
	}
}

func TestResolveProfileEndpoint_AllowlistDenied(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{"dev.nats":"nats://evil.com:4222"}`)
	os.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{"dev.nats":["safe.local"]}`)
	defer func() {
		os.Unsetenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON")
		os.Unsetenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON")
	}()
	result := resolveProfileEndpoint(nil, "dev.nats")
	if result != "" {
		t.Fatalf("expected empty endpoint when host not in allowlist, got %q", result)
	}
}

func TestProfileEndpointAllowed_NoAllowlist(t *testing.T) {
	os.Unsetenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON")
	// No allowlist → allow all
	if !profileEndpointAllowed("dev.nats", "nats://any.host:4222") {
		t.Fatal("expected allowed when no host allowlist configured")
	}
}

func TestProfileEndpointAllowed_InvalidAllowlistJSON(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{bad`)
	defer os.Unsetenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON")
	if profileEndpointAllowed("dev.nats", "nats://any.host:4222") {
		t.Fatal("expected denied when allowlist JSON is invalid")
	}
}

func TestProfileEndpointAllowed_ProfileNotInAllowlist(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{"dev.redis":["redis.local"]}`)
	defer os.Unsetenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON")
	if profileEndpointAllowed("dev.nats", "nats://any.host:4222") {
		t.Fatal("expected denied when profile not in allowlist")
	}
}

func TestProfileEndpointAllowed_EmptyEndpoint(t *testing.T) {
	os.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{"dev.nats":["localhost"]}`)
	defer os.Unsetenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON")
	if profileEndpointAllowed("dev.nats", "") {
		t.Fatal("expected denied for empty endpoint")
	}
}
