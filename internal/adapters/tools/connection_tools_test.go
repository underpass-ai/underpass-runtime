package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestConnListProfiles_DefaultProfiles(t *testing.T) {
	handler := NewConnListProfilesHandler()
	session := domain.Session{AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected conn.list_profiles error: %#v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	count, ok := output["count"].(int)
	if !ok || count < 5 {
		t.Fatalf("unexpected count: %#v", output["count"])
	}
}

func TestConnListProfiles_FilteredByAllowlist(t *testing.T) {
	handler := NewConnListProfilesHandler()
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.redis",
		},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected conn.list_profiles error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["count"] != 1 {
		t.Fatalf("expected one profile, got %#v", output["count"])
	}
}

func TestConnDescribeProfile_ValidationAndNotFound(t *testing.T) {
	handler := NewConnDescribeProfileHandler()
	session := domain.Session{AllowedPaths: []string{"."}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid argument error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"missing"}`))
	if err == nil || err.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not found error, got %#v", err)
	}
}

func TestConnDescribeProfile_Success(t *testing.T) {
	handler := NewConnDescribeProfileHandler()
	session := domain.Session{
		AllowedPaths: []string{"."},
		Metadata: map[string]string{
			"allowed_profiles": "dev.redis",
		},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"profile_id":"dev.redis"}`))
	if err != nil {
		t.Fatalf("unexpected conn.describe_profile error: %#v", err)
	}

	output := result.Output.(map[string]any)
	profile, ok := output["profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected profile map, got %#v", output["profile"])
	}
	if profile["id"] != "dev.redis" {
		t.Fatalf("unexpected profile id: %#v", profile["id"])
	}
}
