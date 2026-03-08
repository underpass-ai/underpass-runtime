package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"reflect"
	"testing"
)

func TestEnvOrDefault(t *testing.T) {
	const key = "WORKSPACE_TEST_ENV_OR_DEFAULT"
	_ = os.Unsetenv(key)
	if value := envOrDefault(key, "fallback"); value != "fallback" {
		t.Fatalf("expected fallback, got %s", value)
	}

	if err := os.Setenv(key, "configured"); err != nil {
		t.Fatalf("setenv failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(key) })
	if value := envOrDefault(key, "fallback"); value != "configured" {
		t.Fatalf("expected configured value, got %s", value)
	}
}

func TestParseLogLevel(t *testing.T) {
	if got := parseLogLevel("debug"); got != slog.LevelDebug {
		t.Fatalf("expected debug, got %v", got)
	}
	if got := parseLogLevel("WARN"); got != slog.LevelWarn {
		t.Fatalf("expected warn, got %v", got)
	}
	if got := parseLogLevel("ERROR"); got != slog.LevelError {
		t.Fatalf("expected error, got %v", got)
	}
	if got := parseLogLevel("unknown"); got != slog.LevelInfo {
		t.Fatalf("expected info fallback, got %v", got)
	}
}

func TestParseIntOrDefault(t *testing.T) {
	if got := parseIntOrDefault("42", 9); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
	if got := parseIntOrDefault("  ", 9); got != 9 {
		t.Fatalf("expected fallback 9, got %d", got)
	}
	if got := parseIntOrDefault("bad", 9); got != 9 {
		t.Fatalf("expected fallback for invalid input, got %d", got)
	}
}

func TestParseStringMapEnv(t *testing.T) {
	parsed, err := parseStringMapEnv(`{"toolchains":"registry.example.com/runner/toolchains:v1","fat":"registry.example.com/runner/fat:v1"}`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	expected := map[string]string{
		"toolchains": "registry.example.com/runner/toolchains:v1",
		"fat":        "registry.example.com/runner/fat:v1",
	}
	if !reflect.DeepEqual(expected, parsed) {
		t.Fatalf("unexpected parsed map: %#v", parsed)
	}

	empty, err := parseStringMapEnv("  ")
	if err != nil {
		t.Fatalf("unexpected empty parse error: %v", err)
	}
	if empty != nil {
		t.Fatalf("expected nil map for empty input, got %#v", empty)
	}
}

func TestParseStringMapEnvRejectsInvalidJSON(t *testing.T) {
	_, err := parseStringMapEnv(`{"toolchains":`)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestBuildInvocationStoreMemoryAndUnsupported(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	const backendKey = "INVOCATION_STORE_BACKEND"
	_ = os.Unsetenv(backendKey)
	store, err := buildInvocationStore(logger)
	if err != nil {
		t.Fatalf("unexpected memory store error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil memory store")
	}

	if err := os.Setenv(backendKey, "unknown-backend"); err != nil {
		t.Fatalf("setenv failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(backendKey) })

	_, err = buildInvocationStore(logger)
	if err == nil {
		t.Fatal("expected unsupported backend error")
	}
}

func TestSetupTelemetryDisabled(t *testing.T) {
	t.Setenv("WORKSPACE_OTEL_ENABLED", "false")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	shutdown, err := setupTelemetry(context.Background(), logger)
	if err != nil {
		t.Fatalf("expected disabled telemetry to initialize cleanly, got %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("unexpected shutdown error: %v", err)
	}
}

func TestParseBoolOrDefault(t *testing.T) {
	cases := []struct {
		raw      string
		fallback bool
		want     bool
	}{
		{"1", false, true},
		{"true", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"0", true, false},
		{"false", true, false},
		{"no", true, false},
		{"off", true, false},
		{"", true, true},
		{"", false, false},
		{"unknown", true, true},
		{"TRUE", false, true},
	}
	for _, tc := range cases {
		got := parseBoolOrDefault(tc.raw, tc.fallback)
		if got != tc.want {
			t.Errorf("parseBoolOrDefault(%q, %v) = %v, want %v", tc.raw, tc.fallback, got, tc.want)
		}
	}
}

func TestBuildSessionStoreMemoryAndUnsupported(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	const backendKey = "SESSION_STORE_BACKEND"
	_ = os.Unsetenv(backendKey)
	store, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("unexpected memory session store error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil memory session store")
	}

	t.Setenv(backendKey, "unknown-backend")
	_, err = buildSessionStore(logger)
	if err == nil {
		t.Fatal("expected unsupported session store backend error")
	}
}

func TestBuildWorkspaceManagerLocalAndErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	workspaceRoot := t.TempDir()
	manager, err := buildWorkspaceManager("local", workspaceRoot, nil, memStore)
	if err != nil {
		t.Fatalf("unexpected local workspace manager error: %v", err)
	}
	if manager == nil {
		t.Fatal("expected non-nil workspace manager")
	}

	// unsupported backend returns error
	_, err = buildWorkspaceManager("unsupported-backend", workspaceRoot, nil, memStore)
	if err == nil {
		t.Fatal("expected error for unsupported workspace backend")
	}
}

func TestInitKubernetesRuntime_LocalBackend(t *testing.T) {
	rt, err := initKubernetesRuntime("local")
	if err != nil {
		t.Fatalf("unexpected error for local backend: %v", err)
	}
	if rt != nil {
		t.Fatal("expected nil runtime for local backend")
	}
}

func TestStartPodJanitorIfEnabled_Local(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	cancel := startPodJanitorIfEnabled("local", "default", nil, nil, logger)
	if cancel != nil {
		t.Fatal("expected nil cancel for local backend")
	}
}

func TestK8sToolHandlers_Local(t *testing.T) {
	handlers := k8sToolHandlers(nil, nil, "default")
	if handlers != nil {
		t.Fatal("expected nil handlers for local backend")
	}
}

func TestBuildCommandRunnerLocal(t *testing.T) {
	runner, err := buildCommandRunner("local", nil)
	if err != nil {
		t.Fatalf("unexpected local command runner error: %v", err)
	}
	if runner == nil {
		t.Fatal("expected non-nil local command runner")
	}
}

func TestParseDisabledBundles(t *testing.T) {
	t.Setenv("WORKSPACE_DISABLED_BUNDLES", "")
	if got := parseDisabledBundles(); got != nil {
		t.Fatalf("expected nil for empty, got %v", got)
	}

	t.Setenv("WORKSPACE_DISABLED_BUNDLES", "messaging,data")
	got := parseDisabledBundles()
	if len(got) != 2 || got[0] != "messaging" || got[1] != "data" {
		t.Fatalf("expected [messaging data], got %v", got)
	}

	t.Setenv("WORKSPACE_DISABLED_BUNDLES", " messaging , , data ")
	got = parseDisabledBundles()
	if len(got) != 2 || got[0] != "messaging" || got[1] != "data" {
		t.Fatalf("expected trimmed [messaging data], got %v", got)
	}
}

func TestBuildToolRegistry_Local(t *testing.T) {
	registry := buildToolRegistry(nil, "default")
	if registry == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestBuildEventBus_None(t *testing.T) {
	t.Setenv("EVENT_BUS", "none")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, nc, stop := buildEventBus(context.Background(), logger)
	if pub == nil {
		t.Fatal("expected non-nil publisher")
	}
	if nc != nil {
		t.Fatal("expected nil nats connection for noop bus")
	}
	if stop != nil {
		t.Fatal("expected nil stop func for noop bus")
	}
}

func TestBuildEventBus_Default(t *testing.T) {
	_ = os.Unsetenv("EVENT_BUS")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, nc, stop := buildEventBus(context.Background(), logger)
	if pub == nil {
		t.Fatal("expected non-nil publisher")
	}
	if nc != nil {
		t.Fatal("expected nil nats connection for default (noop) bus")
	}
	if stop != nil {
		t.Fatal("expected nil stop func for default bus")
	}
}

func TestBuildEventBus_NATSFallbackToNoop(t *testing.T) {
	// No NATS server running — should fall back to noop
	t.Setenv("EVENT_BUS", "nats")
	t.Setenv("EVENT_BUS_NATS_URL", "nats://127.0.0.1:14222") // unreachable port
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, nc, stop := buildEventBus(context.Background(), logger)
	if pub == nil {
		t.Fatal("expected non-nil publisher (noop fallback)")
	}
	if nc != nil {
		t.Fatal("expected nil nats connection on fallback")
	}
	if stop != nil {
		t.Fatal("expected nil stop func on fallback")
	}
}

func TestBuildOutboxRelay_Disabled(t *testing.T) {
	t.Setenv("EVENT_BUS_OUTBOX", "false")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, stop := buildOutboxRelay(context.Background(), logger, nil)
	if pub != nil {
		t.Fatal("expected nil publisher when outbox disabled")
	}
	if stop != nil {
		t.Fatal("expected nil stop func when outbox disabled")
	}
}

func TestBuildOutboxRelay_DefaultDisabled(t *testing.T) {
	_ = os.Unsetenv("EVENT_BUS_OUTBOX")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, stop := buildOutboxRelay(context.Background(), logger, nil)
	if pub != nil {
		t.Fatal("expected nil publisher when outbox not set")
	}
	if stop != nil {
		t.Fatal("expected nil stop func when outbox not set")
	}
}

func TestBuildOutboxRelay_EnabledNoValkey(t *testing.T) {
	t.Setenv("EVENT_BUS_OUTBOX", "true")
	t.Setenv("VALKEY_ADDR", "127.0.0.1:16379") // unreachable
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, stop := buildOutboxRelay(context.Background(), logger, nil)
	if pub != nil {
		t.Fatal("expected nil publisher when valkey unreachable")
	}
	if stop != nil {
		t.Fatal("expected nil stop func when valkey unreachable")
	}
}

func TestBuildOutboxRelay_EnabledHostPort(t *testing.T) {
	t.Setenv("EVENT_BUS_OUTBOX", "true")
	_ = os.Unsetenv("VALKEY_ADDR")
	t.Setenv("VALKEY_HOST", "127.0.0.1")
	t.Setenv("VALKEY_PORT", "16379") // unreachable
	t.Setenv("EVENT_BUS_OUTBOX_KEY_PREFIX", "test:outbox")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, stop := buildOutboxRelay(context.Background(), logger, nil)
	if pub != nil {
		t.Fatal("expected nil publisher when valkey unreachable via host:port")
	}
	if stop != nil {
		t.Fatal("expected nil stop func when valkey unreachable via host:port")
	}
}

func TestBuildEventBus_UnknownValue(t *testing.T) {
	t.Setenv("EVENT_BUS", "kafka") // unsupported, falls to default
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	pub, nc, stop := buildEventBus(context.Background(), logger)
	if pub == nil {
		t.Fatal("expected non-nil publisher for unknown bus type")
	}
	if nc != nil {
		t.Fatal("expected nil nats connection for unknown bus type")
	}
	if stop != nil {
		t.Fatal("expected nil stop for unknown bus type")
	}
}

func TestBuildArtifactStore_Local(t *testing.T) {
	t.Setenv("ARTIFACT_BACKEND", "local")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	store, err := buildArtifactStore(context.Background(), t.TempDir(), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestBuildArtifactStore_Default(t *testing.T) {
	_ = os.Unsetenv("ARTIFACT_BACKEND")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	store, err := buildArtifactStore(context.Background(), t.TempDir(), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestBuildArtifactStore_Unsupported(t *testing.T) {
	t.Setenv("ARTIFACT_BACKEND", "gcs")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	_, err := buildArtifactStore(context.Background(), t.TempDir(), logger)
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestBuildArtifactStore_S3(t *testing.T) {
	t.Setenv("ARTIFACT_BACKEND", "s3")
	t.Setenv("ARTIFACT_S3_BUCKET", "test-bucket")
	t.Setenv("ARTIFACT_S3_REGION", "us-west-2")
	t.Setenv("ARTIFACT_S3_ACCESS_KEY", "testkey")
	t.Setenv("ARTIFACT_S3_SECRET_KEY", "testsecret")
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	store, err := buildArtifactStore(context.Background(), t.TempDir(), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil S3 store")
	}
}
