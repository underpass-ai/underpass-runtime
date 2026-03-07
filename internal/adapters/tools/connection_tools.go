package tools

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type ConnListProfilesHandler struct{}

type ConnDescribeProfileHandler struct{}

type connectionProfile struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	ReadOnly    bool           `json:"read_only"`
	Scopes      map[string]any `json:"scopes,omitempty"`
}

func NewConnListProfilesHandler() *ConnListProfilesHandler {
	return &ConnListProfilesHandler{}
}

func NewConnDescribeProfileHandler() *ConnDescribeProfileHandler {
	return &ConnDescribeProfileHandler{}
}

func (h *ConnListProfilesHandler) Name() string {
	return "conn.list_profiles"
}

func (h *ConnListProfilesHandler) Invoke(_ context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	if len(args) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(args, &payload); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid conn.list_profiles args",
				Retryable: false,
			}
		}
	}

	profiles := filterProfilesByAllowlist(resolveConnectionProfiles(session), session.Metadata)
	outputProfiles := make([]map[string]any, 0, len(profiles))
	for _, profile := range profiles {
		outputProfiles = append(outputProfiles, mapProfileOutput(profile))
	}

	return app.ToolRunResult{
		Output: map[string]any{
			"profiles": outputProfiles,
			"count":    len(outputProfiles),
		},
		Logs: []domain.LogLine{{
			At:      time.Now().UTC(),
			Channel: "stdout",
			Message: "listed connection profiles",
		}},
	}, nil
}

func (h *ConnDescribeProfileHandler) Name() string {
	return "conn.describe_profile"
}

func (h *ConnDescribeProfileHandler) Invoke(_ context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ProfileID string `json:"profile_id"`
	}{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "invalid conn.describe_profile args",
				Retryable: false,
			}
		}
	}

	profileID := strings.TrimSpace(request.ProfileID)
	if profileID == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "profile_id is required",
			Retryable: false,
		}
	}

	profiles := filterProfilesByAllowlist(resolveConnectionProfiles(session), session.Metadata)
	for _, profile := range profiles {
		if profile.ID != profileID {
			continue
		}
		return app.ToolRunResult{
			Output: map[string]any{
				"profile": mapProfileOutput(profile),
			},
			Logs: []domain.LogLine{{
				At:      time.Now().UTC(),
				Channel: "stdout",
				Message: "described connection profile",
			}},
		}, nil
	}

	return app.ToolRunResult{}, &domain.Error{
		Code:      app.ErrorCodeNotFound,
		Message:   "connection profile not found",
		Retryable: false,
	}
}

func resolveConnectionProfiles(session domain.Session) []connectionProfile {
	if session.Metadata != nil {
		if raw := strings.TrimSpace(session.Metadata["connection_profiles_json"]); raw != "" {
			var fromMetadata []connectionProfile
			if err := json.Unmarshal([]byte(raw), &fromMetadata); err == nil && len(fromMetadata) > 0 {
				return fromMetadata
			}
		}
	}
	return defaultConnectionProfiles()
}

func filterProfilesByAllowlist(profiles []connectionProfile, metadata map[string]string) []connectionProfile {
	allowed := parseProfileAllowlist(metadata)
	if len(allowed) == 0 || allowed["*"] {
		return profiles
	}

	filtered := make([]connectionProfile, 0, len(profiles))
	for _, profile := range profiles {
		if allowed[profile.ID] {
			filtered = append(filtered, profile)
		}
	}
	return filtered
}

func parseProfileAllowlist(metadata map[string]string) map[string]bool {
	if len(metadata) == 0 {
		return map[string]bool{}
	}
	raw := strings.TrimSpace(metadata["allowed_profiles"])
	if raw == "" {
		return map[string]bool{}
	}

	allowed := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		candidate := strings.TrimSpace(item)
		if candidate == "" {
			continue
		}
		allowed[candidate] = true
	}
	return allowed
}

func mapProfileOutput(profile connectionProfile) map[string]any {
	return map[string]any{
		"id":          profile.ID,
		"kind":        profile.Kind,
		"description": profile.Description,
		"read_only":   profile.ReadOnly,
		"scopes":      profile.Scopes,
	}
}

func defaultConnectionProfiles() []connectionProfile {
	return []connectionProfile{
		{
			ID:          "dev.nats",
			Kind:        "nats",
			Description: "NATS profile for sandbox/dev namespaces",
			ReadOnly:    true,
			Scopes: map[string]any{
				"subjects": []string{"sandbox.>", "dev.>"},
			},
		},
		{
			ID:          "dev.kafka",
			Kind:        "kafka",
			Description: "Kafka profile for sandbox/dev topics",
			ReadOnly:    true,
			Scopes: map[string]any{
				"topics": []string{"sandbox.", "dev."},
			},
		},
		{
			ID:          "dev.rabbit",
			Kind:        "rabbitmq",
			Description: "RabbitMQ profile for sandbox/dev queues",
			ReadOnly:    true,
			Scopes: map[string]any{
				"queues": []string{"sandbox.", "dev."},
			},
		},
		{
			ID:          "dev.redis",
			Kind:        "redis",
			Description: "Redis profile for sandbox/dev key prefixes",
			ReadOnly:    true,
			Scopes: map[string]any{
				"key_prefixes": []string{"sandbox:", "dev:"},
			},
		},
		{
			ID:          "dev.mongo",
			Kind:        "mongo",
			Description: "Mongo profile for sandbox/dev databases",
			ReadOnly:    true,
			Scopes: map[string]any{
				"databases": []string{"sandbox", "dev"},
			},
		},
	}
}

// resolveTypedProfile resolves a connection profile by ID, validating that the
// profile kind matches one of the allowed kinds. If the endpoint is empty and
// the profile ID matches defaultID, the defaultEndpoint is used as fallback.
func resolveTypedProfile(session domain.Session, requestedID string,
	allowedKinds []string, defaultID, defaultEndpoint string,
) (connectionProfile, string, *domain.Error) {
	profileID := strings.TrimSpace(requestedID)
	if profileID == "" {
		return connectionProfile{}, "", &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "profile_id is required",
			Retryable: false,
		}
	}

	profiles := filterProfilesByAllowlist(resolveConnectionProfiles(session), session.Metadata)
	for _, p := range profiles {
		if p.ID != profileID {
			continue
		}
		kindLower := strings.ToLower(strings.TrimSpace(p.Kind))
		matched := false
		for _, k := range allowedKinds {
			if kindLower == k {
				matched = true
				break
			}
		}
		if !matched {
			return connectionProfile{}, "", &domain.Error{
				Code:      app.ErrorCodeInvalidArgument,
				Message:   "profile kind does not match expected type",
				Retryable: false,
			}
		}

		endpoint := resolveProfileEndpoint(session.Metadata, profileID)
		if endpoint == "" && defaultID != "" && profileID == defaultID {
			endpoint = defaultEndpoint
		}
		if strings.TrimSpace(endpoint) == "" {
			return connectionProfile{}, "", &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "profile endpoint not configured",
				Retryable: false,
			}
		}
		return p, endpoint, nil
	}

	return connectionProfile{}, "", &domain.Error{
		Code:      app.ErrorCodeNotFound,
		Message:   "connection profile not found",
		Retryable: false,
	}
}

func clampInt(value, min, max, fallback int) int {
	if value == 0 {
		value = fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func resolveProfileEndpoint(_ map[string]string, profileID string) string {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_CONN_PROFILE_ENDPOINTS_JSON"))
	if raw == "" {
		return ""
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ""
	}
	endpoint := strings.TrimSpace(parsed[profileID])
	if endpoint == "" {
		return ""
	}
	if !profileEndpointAllowed(profileID, endpoint) {
		return ""
	}
	return endpoint
}

func profileEndpointAllowed(profileID, endpoint string) bool {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_CONN_PROFILE_HOST_ALLOWLIST_JSON"))
	if raw == "" {
		return true
	}

	var allowlist map[string][]string
	if err := json.Unmarshal([]byte(raw), &allowlist); err != nil {
		return false
	}
	rules, found := allowlist[strings.TrimSpace(profileID)]
	if !found || len(rules) == 0 {
		return false
	}

	host := endpointHost(endpoint)
	if host == "" {
		return false
	}
	for _, rule := range rules {
		if hostMatchesAllowRule(host, rule) {
			return true
		}
	}
	return false
}

func endpointHost(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed != nil && strings.TrimSpace(parsed.Hostname()) != "" {
		return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	}

	parsed, err = url.Parse("//" + trimmed)
	if err != nil || parsed == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func hostMatchesAllowRule(host, rawRule string) bool {
	rule := strings.ToLower(strings.TrimSpace(rawRule))
	if rule == "" {
		return false
	}

	if _, cidr, err := net.ParseCIDR(rule); err == nil {
		ip := net.ParseIP(host)
		return ip != nil && cidr.Contains(ip)
	}
	if strings.HasPrefix(rule, "*.") {
		suffix := strings.TrimPrefix(rule, "*.")
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	return host == rule
}
