package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeNATSClient struct {
	request func(serverURL, subject string, payload []byte, timeout time.Duration) ([]byte, error)
	publish func(serverURL, subject string, payload []byte, timeout time.Duration) error
	pull    func(serverURL, subject string, timeout time.Duration, maxMessages int) ([]natsMessage, error)
}

func (f *fakeNATSClient) Request(_ context.Context, serverURL, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	if f.request != nil {
		return f.request(serverURL, subject, payload, timeout)
	}
	return []byte("ok"), nil
}

func (f *fakeNATSClient) Publish(_ context.Context, serverURL, subject string, payload []byte, timeout time.Duration) error {
	if f.publish != nil {
		return f.publish(serverURL, subject, payload, timeout)
	}
	return nil
}

func (f *fakeNATSClient) SubscribePull(_ context.Context, serverURL, subject string, timeout time.Duration, maxMessages int) ([]natsMessage, error) {
	if f.pull != nil {
		return f.pull(serverURL, subject, timeout, maxMessages)
	}
	return []natsMessage{}, nil
}

func TestNATSRequestHandler_Success(t *testing.T) {
	handler := NewNATSRequestHandler(&fakeNATSClient{
		request: func(serverURL, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
			if serverURL == "" || subject == "" || timeout <= 0 {
				t.Fatalf("unexpected request params: %q %q %v", serverURL, subject, timeout)
			}
			if string(payload) != "hello" {
				t.Fatalf("unexpected payload: %q", string(payload))
			}
			return []byte("response"), nil
		},
	})

	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.nats",
		},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.nats","subject":"sandbox.echo","payload":"hello","timeout_ms":500,"max_bytes":16}`))
	if err != nil {
		t.Fatalf("unexpected nats.request error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["profile_id"] != "dev.nats" {
		t.Fatalf("unexpected profile_id: %#v", output["profile_id"])
	}
	if output["subject"] != "sandbox.echo" {
		t.Fatalf("unexpected subject: %#v", output["subject"])
	}
	encoded := output["response_base64"].(string)
	decoded, decErr := base64.StdEncoding.DecodeString(encoded)
	if decErr != nil || string(decoded) != "response" {
		t.Fatalf("unexpected response data: %q err=%v", encoded, decErr)
	}
}

func TestNATSRequestHandler_DeniesSubjectOutsideProfileScope(t *testing.T) {
	handler := NewNATSRequestHandler(&fakeNATSClient{})
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.nats",
		},
	}
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.nats","subject":"prod.secret"}`))
	if err == nil {
		t.Fatal("expected policy error")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestNATSPublishHandler_Success(t *testing.T) {
	handler := NewNATSPublishHandler(&fakeNATSClient{
		publish: func(serverURL, subject string, payload []byte, timeout time.Duration) error {
			if serverURL == "" || subject != "sandbox.events" || timeout <= 0 {
				t.Fatalf("unexpected publish params: %q %q %v", serverURL, subject, timeout)
			}
			if string(payload) != "hello" {
				t.Fatalf("unexpected payload: %q", string(payload))
			}
			return nil
		},
	})

	session := writableNATSSession()
	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.nats","subject":"sandbox.events","payload":"hello","timeout_ms":500}`))
	if err != nil {
		t.Fatalf("unexpected nats.publish error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["published"] != true {
		t.Fatalf("expected published=true, got %#v", output["published"])
	}
}

func TestNATSPublishHandler_DeniesReadOnlyProfile(t *testing.T) {
	handler := NewNATSPublishHandler(&fakeNATSClient{})
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.nats",
		},
	}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.nats","subject":"sandbox.events","payload":"hello","timeout_ms":500}`))
	if err == nil {
		t.Fatal("expected read_only policy denial")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
	if err.Message != "profile is read_only" {
		t.Fatalf("unexpected error message: %q", err.Message)
	}
}

func TestNATSPublishHandler_ExecutionError(t *testing.T) {
	handler := NewNATSPublishHandler(&fakeNATSClient{
		publish: func(serverURL, subject string, payload []byte, timeout time.Duration) error {
			return errors.New("nats down")
		},
	})

	_, err := handler.Invoke(context.Background(), writableNATSSession(), json.RawMessage(`{"profile_id":"dev.nats","subject":"sandbox.events","payload":"hello","timeout_ms":500}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestNATSSubscribePullHandler_Success(t *testing.T) {
	handler := NewNATSSubscribePullHandler(&fakeNATSClient{
		pull: func(serverURL, subject string, timeout time.Duration, maxMessages int) ([]natsMessage, error) {
			return []natsMessage{
				{Subject: subject, Data: []byte("m1")},
				{Subject: subject, Data: []byte("m2")},
			}, nil
		},
	})
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.nats",
		},
	}
	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.nats","subject":"sandbox.jobs","max_messages":2,"max_bytes":16}`))
	if err != nil {
		t.Fatalf("unexpected nats.subscribe_pull error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["message_count"] != 2 {
		t.Fatalf("unexpected message_count: %#v", output["message_count"])
	}
}

func TestNATSSubscribePullHandler_ExecutionError(t *testing.T) {
	handler := NewNATSSubscribePullHandler(&fakeNATSClient{
		pull: func(serverURL, subject string, timeout time.Duration, maxMessages int) ([]natsMessage, error) {
			return nil, errors.New("connection down")
		},
	})
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.nats",
		},
	}
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.nats","subject":"sandbox.jobs"}`))
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
}

func TestNATSHandlers_NamesAndLiveClientErrors(t *testing.T) {
	if NewNATSRequestHandler(nil).Name() != "nats.request" {
		t.Fatal("unexpected nats.request name")
	}
	if NewNATSPublishHandler(nil).Name() != "nats.publish" {
		t.Fatal("unexpected nats.publish name")
	}
	if NewNATSSubscribePullHandler(nil).Name() != "nats.subscribe_pull" {
		t.Fatal("unexpected nats.subscribe_pull name")
	}

	client := &liveNATSClient{}
	ctx := context.Background()
	if _, err := client.Request(ctx, "://bad-url", "sandbox.echo", []byte("x"), 5*time.Millisecond); err == nil {
		t.Fatal("expected live NATS request error for invalid url")
	}
	if err := client.Publish(ctx, "://bad-url", "sandbox.echo", []byte("x"), 5*time.Millisecond); err == nil {
		t.Fatal("expected live NATS publish error for invalid url")
	}
	if _, err := client.SubscribePull(ctx, "://bad-url", "sandbox.echo", 5*time.Millisecond, 1); err == nil {
		t.Fatal("expected live NATS subscribe error for invalid url")
	}
}

func writableNATSSession() domain.Session {
	return domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles":         "dev.nats",
			"connection_profiles_json": `[{"id":"dev.nats","kind":"nats","read_only":false,"scopes":{"subjects":["sandbox.>","dev.>"]}}]`,
		},
	}
}

func TestNATSProfileAndPayloadHelpers(t *testing.T) {
	_, _, err := resolveNATSProfile(domain.Session{}, "")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected profile_id validation error, got %#v", err)
	}

	sessionWrongKind := domain.Session{
		Metadata: map[string]string{
			"connection_profiles_json": `[{"id":"x","kind":"redis","read_only":true,"scopes":{"subjects":["sandbox.>"]}}]`,
		},
	}
	_, _, err = resolveNATSProfile(sessionWrongKind, "x")
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected wrong kind validation, got %#v", err)
	}

	profile := connectionProfile{
		ID:     "dev.nats",
		Kind:   "nats",
		Scopes: map[string]any{"subjects": []any{"sandbox.>", "dev.*"}},
	}
	if !subjectAllowedByProfile("sandbox.jobs", profile) {
		t.Fatal("expected subject to be allowed by profile")
	}
	if subjectAllowedByProfile("prod.jobs", profile) {
		t.Fatal("did not expect prod subject to be allowed")
	}

	if !natsSubjectPatternMatch("sandbox.>", "sandbox.jobs.created") {
		t.Fatal("expected > wildcard subject match")
	}
	if !natsSubjectPatternMatch("sandbox.*.created", "sandbox.jobs.created") {
		t.Fatal("expected * wildcard subject match")
	}

	raw, decErr := decodePayload(base64.StdEncoding.EncodeToString([]byte("hello")), "base64")
	if decErr != nil || string(raw) != "hello" {
		t.Fatalf("unexpected decodePayload base64 result: raw=%q err=%v", string(raw), decErr)
	}
	if _, decErr = decodePayload("%%%bad", "base64"); decErr == nil {
		t.Fatal("expected decodePayload base64 validation error")
	}
	if _, decErr = decodePayload("hello", "hex"); decErr == nil {
		t.Fatal("expected decodePayload unsupported encoding error")
	}
}

func TestResolveProfileEndpoint_IgnoresMetadataOverride(t *testing.T) {
	t.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", "")

	endpoint := resolveProfileEndpoint(
		map[string]string{
			"connection_profile_endpoints_json": `{"dev.nats":"nats://metadata:4222"}`,
		},
		"dev.nats",
	)
	if endpoint != "" {
		t.Fatalf("expected metadata endpoint to be ignored, got %q", endpoint)
	}
}

func TestResolveProfileEndpoint_UsesServerEnv(t *testing.T) {
	t.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{"dev.nats":"nats://env:4222"}`)
	t.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", "")

	endpoint := resolveProfileEndpoint(
		map[string]string{
			"connection_profile_endpoints_json": `{"dev.nats":"nats://metadata:4222"}`,
		},
		"dev.nats",
	)
	if endpoint != "nats://env:4222" {
		t.Fatalf("expected server-side endpoint to be used, got %q", endpoint)
	}
}

func TestResolveProfileEndpoint_AllowlistRejectsDisallowedHost(t *testing.T) {
	t.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{"dev.nats":"nats://nats.internal:4222"}`)
	t.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{"dev.nats":["sandbox.nats.internal"]}`)

	endpoint := resolveProfileEndpoint(map[string]string{}, "dev.nats")
	if endpoint != "" {
		t.Fatalf("expected endpoint to be rejected by allowlist, got %q", endpoint)
	}
}

func TestClampInt_AllBranches(t *testing.T) {
	// zero value → use fallback
	if got := clampInt(0, 1, 100, 50); got != 50 {
		t.Fatalf("expected fallback 50, got %d", got)
	}
	// below min → clamp to min
	if got := clampInt(-5, 1, 100, 50); got != 1 {
		t.Fatalf("expected min 1, got %d", got)
	}
	// above max → clamp to max
	if got := clampInt(200, 1, 100, 50); got != 100 {
		t.Fatalf("expected max 100, got %d", got)
	}
	// within range → return as-is
	if got := clampInt(42, 1, 100, 50); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestNATSSubscribePullHandler_ValidationPaths(t *testing.T) {
	handler := NewNATSSubscribePullHandler(&fakeNATSClient{})
	readOnlySession := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.nats",
		},
	}

	// empty subject
	_, err := handler.Invoke(context.Background(), readOnlySession, json.RawMessage(`{"profile_id":"dev.nats","subject":""}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty subject, got %#v", err)
	}

	// profile not found
	_, err = handler.Invoke(context.Background(), readOnlySession, json.RawMessage(`{"profile_id":"unknown","subject":"sandbox.jobs"}`))
	if err == nil || err.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not_found for unknown profile, got %#v", err)
	}

	// subject outside allowlist
	_, err = handler.Invoke(context.Background(), readOnlySession, json.RawMessage(`{"profile_id":"dev.nats","subject":"prod.secret"}`))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy_denied for out-of-scope subject, got %#v", err)
	}
}

func TestResolveProfileEndpoint_AllowlistAllowsWildcardAndCIDR(t *testing.T) {
	t.Setenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON", `{"dev.nats":"nats://mq.dev.svc.cluster.local:4222","dev.redis":"10.0.1.22:6379"}`)
	t.Setenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON", `{"dev.nats":["*.svc.cluster.local"],"dev.redis":["10.0.0.0/8"]}`)

	natsEndpoint := resolveProfileEndpoint(map[string]string{}, "dev.nats")
	if natsEndpoint == "" {
		t.Fatal("expected wildcard host rule to allow nats endpoint")
	}
	redisEndpoint := resolveProfileEndpoint(map[string]string{}, "dev.redis")
	if redisEndpoint == "" {
		t.Fatal("expected cidr rule to allow redis endpoint")
	}
}
