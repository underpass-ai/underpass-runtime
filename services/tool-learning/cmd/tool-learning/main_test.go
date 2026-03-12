package main

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}

	for _, tt := range tests {
		got := parseLogLevel(tt.input)
		if got != tt.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestEnvOrDefault(t *testing.T) {
	os.Unsetenv("TEST_ENV_OR_DEFAULT_KEY")
	got := envOrDefault("TEST_ENV_OR_DEFAULT_KEY", "fallback")
	if got != "fallback" {
		t.Errorf("envOrDefault(unset) = %q, want fallback", got)
	}

	os.Setenv("TEST_ENV_OR_DEFAULT_KEY", "custom")
	defer os.Unsetenv("TEST_ENV_OR_DEFAULT_KEY")
	got = envOrDefault("TEST_ENV_OR_DEFAULT_KEY", "fallback")
	if got != "custom" {
		t.Errorf("envOrDefault(set) = %q, want custom", got)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	// Clear env vars to test defaults
	for _, key := range []string{"S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_REGION",
		"S3_USE_SSL", "LAKE_BUCKET", "AUDIT_BUCKET", "VALKEY_ADDR",
		"VALKEY_PASSWORD", "VALKEY_DB", "VALKEY_KEY_PREFIX", "VALKEY_TTL", "NATS_URL"} {
		os.Unsetenv(key)
	}

	cfg, err := loadConfig("hourly")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.S3Endpoint != "localhost:9000" {
		t.Errorf("S3Endpoint = %q, want localhost:9000", cfg.S3Endpoint)
	}
	if cfg.S3Region != "us-east-1" {
		t.Errorf("S3Region = %q, want us-east-1", cfg.S3Region)
	}
	if cfg.S3UseSSL {
		t.Error("S3UseSSL should be false by default")
	}
	if cfg.LakeBucket != "telemetry-lake" {
		t.Errorf("LakeBucket = %q, want telemetry-lake", cfg.LakeBucket)
	}
	if cfg.AuditBucket != "policy-audit" {
		t.Errorf("AuditBucket = %q, want policy-audit", cfg.AuditBucket)
	}
	if cfg.ValkeyAddr != "localhost:6379" {
		t.Errorf("ValkeyAddr = %q, want localhost:6379", cfg.ValkeyAddr)
	}
	if cfg.ValkeyDB != 0 {
		t.Errorf("ValkeyDB = %d, want 0", cfg.ValkeyDB)
	}
	if cfg.ValkeyPfx != "tool_policy" {
		t.Errorf("ValkeyPfx = %q, want tool_policy", cfg.ValkeyPfx)
	}
	if cfg.ValkeyTTL != 2*time.Hour {
		t.Errorf("ValkeyTTL = %v, want 2h", cfg.ValkeyTTL)
	}
	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q, want nats://localhost:4222", cfg.NATSURL)
	}
	if cfg.Schedule != "hourly" {
		t.Errorf("Schedule = %q, want hourly", cfg.Schedule)
	}
}

func TestLoadConfigCustom(t *testing.T) {
	os.Setenv("S3_ENDPOINT", "minio:9000")
	os.Setenv("S3_ACCESS_KEY", "mykey")
	os.Setenv("S3_SECRET_KEY", "mysecret")
	os.Setenv("S3_REGION", "eu-west-1")
	os.Setenv("S3_USE_SSL", "true")
	os.Setenv("LAKE_BUCKET", "my-lake")
	os.Setenv("AUDIT_BUCKET", "my-audit")
	os.Setenv("VALKEY_ADDR", "redis:6380")
	os.Setenv("VALKEY_PASSWORD", "pass123")
	os.Setenv("VALKEY_DB", "3")
	os.Setenv("VALKEY_KEY_PREFIX", "custom_prefix")
	os.Setenv("VALKEY_TTL", "30m")
	os.Setenv("NATS_URL", "nats://nats:4222")
	defer func() {
		for _, key := range []string{"S3_ENDPOINT", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_REGION",
			"S3_USE_SSL", "LAKE_BUCKET", "AUDIT_BUCKET", "VALKEY_ADDR",
			"VALKEY_PASSWORD", "VALKEY_DB", "VALKEY_KEY_PREFIX", "VALKEY_TTL", "NATS_URL"} {
			os.Unsetenv(key)
		}
	}()

	cfg, err := loadConfig("daily")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.S3Endpoint != "minio:9000" {
		t.Errorf("S3Endpoint = %q, want minio:9000", cfg.S3Endpoint)
	}
	if cfg.S3AccessKey != "mykey" {
		t.Errorf("S3AccessKey = %q, want mykey", cfg.S3AccessKey)
	}
	if !cfg.S3UseSSL {
		t.Error("S3UseSSL should be true")
	}
	if cfg.ValkeyAddr != "redis:6380" {
		t.Errorf("ValkeyAddr = %q, want redis:6380", cfg.ValkeyAddr)
	}
	if cfg.ValkeyPass != "pass123" {
		t.Errorf("ValkeyPass = %q, want pass123", cfg.ValkeyPass)
	}
	if cfg.ValkeyDB != 3 {
		t.Errorf("ValkeyDB = %d, want 3", cfg.ValkeyDB)
	}
	if cfg.ValkeyTTL != 30*time.Minute {
		t.Errorf("ValkeyTTL = %v, want 30m", cfg.ValkeyTTL)
	}
	if cfg.Schedule != "daily" {
		t.Errorf("Schedule = %q, want daily", cfg.Schedule)
	}
}

func TestLoadConfigInvalidValkeyDB(t *testing.T) {
	t.Setenv("VALKEY_DB", "not-a-number")
	_, err := loadConfig("hourly")
	if err == nil {
		t.Fatal("expected error for invalid VALKEY_DB")
	}
}

func TestLoadConfigInvalidValkeyTTL(t *testing.T) {
	t.Setenv("VALKEY_TTL", "not-a-duration")
	_, err := loadConfig("hourly")
	if err == nil {
		t.Fatal("expected error for invalid VALKEY_TTL")
	}
}
