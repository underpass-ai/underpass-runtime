package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"reflect"
	"testing"

	k8sfake "k8s.io/client-go/kubernetes/fake"
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
		{"TRUE", false, true}, // toLower("TRUE") = "true" → returns true
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

	// local backend creates the workspace root if needed
	workspaceRoot := t.TempDir()
	manager, err := buildWorkspaceManager("local", workspaceRoot, nil, memStore)
	if err != nil {
		t.Fatalf("unexpected local workspace manager error: %v", err)
	}
	if manager == nil {
		t.Fatal("expected non-nil workspace manager")
	}

	// kubernetes backend with nil client returns error
	_, err = buildWorkspaceManager("kubernetes", workspaceRoot, nil, memStore)
	if err == nil {
		t.Fatal("expected error for kubernetes backend with nil client")
	}

	// unsupported backend returns error
	_, err = buildWorkspaceManager("unsupported-backend", workspaceRoot, nil, memStore)
	if err == nil {
		t.Fatal("expected error for unsupported workspace backend")
	}
}

func TestBuildWorkspaceManagerKubernetes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	memStore, err := buildSessionStore(logger)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	kubeClient := k8sfake.NewSimpleClientset()
	manager, err := buildWorkspaceManager("kubernetes", t.TempDir(), kubeClient, memStore)
	if err != nil {
		t.Fatalf("unexpected kubernetes workspace manager error: %v", err)
	}
	if manager == nil {
		t.Fatal("expected non-nil kubernetes workspace manager")
	}
}

func TestBuildCommandRunnerLocalAndErrors(t *testing.T) {
	// local backend returns a local command runner
	runner, err := buildCommandRunner("local", nil, nil)
	if err != nil {
		t.Fatalf("unexpected local command runner error: %v", err)
	}
	if runner == nil {
		t.Fatal("expected non-nil local command runner")
	}

	// kubernetes backend with nil client and nil config returns error
	_, err = buildCommandRunner("kubernetes", nil, nil)
	if err == nil {
		t.Fatal("expected error for kubernetes runner with nil client and config")
	}

	// kubernetes backend with fake client but nil config still returns error
	kubeClient := k8sfake.NewSimpleClientset()
	_, err = buildCommandRunner("kubernetes", kubeClient, nil)
	if err == nil {
		t.Fatal("expected error for kubernetes runner with nil rest config")
	}
}
