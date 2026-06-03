package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	notifyEscalationRoutesEnv  = "WORKSPACE_NOTIFY_ESCALATION_ROUTES_JSON"
	notifyDefaultProvider      = "webhook"
	notifyRateLimitWindow      = time.Minute
	notifyEscalationMaxRetries = 1
)

type NotifyEscalationRoute struct {
	Channel    string `json:"channel"`
	Provider   string `json:"provider"`
	WebhookURL string `json:"webhook_url"`
}

type NotifyEscalationChannelHandler struct {
	client     *http.Client
	routes     map[string]NotifyEscalationRoute
	configErr  error
	now        func() time.Time
	maxRetries int

	mu           sync.Mutex
	lastDelivery map[string]time.Time
}

func NewNotifyEscalationChannelHandler(routes map[string]NotifyEscalationRoute, client *http.Client) *NotifyEscalationChannelHandler {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	copied := make(map[string]NotifyEscalationRoute, len(routes))
	for key, value := range routes {
		copied[strings.ToLower(strings.TrimSpace(key))] = value
	}
	return &NotifyEscalationChannelHandler{
		client:       client,
		routes:       copied,
		now:          func() time.Time { return time.Now().UTC() },
		maxRetries:   notifyEscalationMaxRetries,
		lastDelivery: map[string]time.Time{},
	}
}

func NewNotifyEscalationChannelHandlerFromEnv() *NotifyEscalationChannelHandler {
	routes, err := loadNotifyEscalationRoutes(os.Getenv(notifyEscalationRoutesEnv))
	handler := NewNotifyEscalationChannelHandler(routes, nil)
	handler.configErr = err
	return handler
}

func (h *NotifyEscalationChannelHandler) Name() string {
	return "notify.escalation_channel"
}

func (h *NotifyEscalationChannelHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	if h.configErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("notify escalation routing config invalid: %v", h.configErr),
			Retryable: false,
		}
	}

	request := struct {
		IncidentID         string `json:"incident_id"`
		HandoffNodeID      string `json:"handoff_node_id"`
		Summary            string `json:"summary"`
		UpstreamSpecialist string `json:"upstream_specialist"`
		UpstreamDecision   string `json:"upstream_decision"`
		Reason             string `json:"reason"`
		ResourceRef        string `json:"resource_ref"`
	}{}
	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid notify.escalation_channel args",
			Retryable: false,
		}
	}
	if strings.TrimSpace(request.IncidentID) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "incident_id is required", Retryable: false}
	}
	if strings.TrimSpace(request.HandoffNodeID) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "handoff_node_id is required", Retryable: false}
	}
	if strings.TrimSpace(request.Summary) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "summary is required", Retryable: false}
	}
	if strings.TrimSpace(request.UpstreamSpecialist) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "upstream_specialist is required", Retryable: false}
	}
	if strings.TrimSpace(request.UpstreamDecision) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "upstream_decision is required", Retryable: false}
	}
	if strings.TrimSpace(request.Reason) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "reason is required", Retryable: false}
	}

	environment := toolRuntimeEnvironment(session.Metadata)
	route, ok := h.routes[environment]
	if !ok {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("notify route not configured for environment %q", environment),
			Retryable: false,
		}
	}
	if strings.TrimSpace(route.WebhookURL) == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("notify route for environment %q is missing webhook_url", environment),
			Retryable: false,
		}
	}

	if ok := h.allowDelivery(request.IncidentID); !ok {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   "rate_limit_exceeded",
			Retryable: false,
		}
	}

	provider := strings.TrimSpace(route.Provider)
	if provider == "" {
		provider = notifyDefaultProvider
	}
	payload := map[string]any{
		"text": fmt.Sprintf(
			"[%s] %s (%s → %s)",
			request.IncidentID,
			request.Summary,
			request.UpstreamSpecialist,
			request.UpstreamDecision,
		),
		"incident_id":         request.IncidentID,
		"handoff_node_id":     request.HandoffNodeID,
		"summary":             request.Summary,
		"upstream_specialist": request.UpstreamSpecialist,
		"upstream_decision":   request.UpstreamDecision,
		"reason":              request.Reason,
		"resource_ref":        request.ResourceRef,
		"environment":         environment,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInternal,
			Message:   fmt.Sprintf("marshal notify payload: %v", err),
			Retryable: false,
		}
	}

	var resp *http.Response
	var deliveryErr *domain.Error
	for attempt := 0; attempt <= h.maxRetries; attempt++ {
		httpReq, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, route.WebhookURL, bytes.NewReader(body))
		if requestErr != nil {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   fmt.Sprintf("build notify request: %v", requestErr),
				Retryable: false,
			}
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err = h.client.Do(httpReq)
		if err != nil {
			deliveryErr = &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   fmt.Sprintf("notify delivery failed: %v", err),
				Retryable: true,
			}
			if attempt < h.maxRetries {
				continue
			}
			return app.ToolRunResult{}, deliveryErr
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			retryable := resp.StatusCode >= http.StatusInternalServerError
			deliveryErr = &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   fmt.Sprintf("notify delivery returned HTTP %d", resp.StatusCode),
				Retryable: retryable,
			}
			if retryable && attempt < h.maxRetries {
				resp.Body.Close() //nolint:errcheck
				continue
			}
			resp.Body.Close() //nolint:errcheck
			return app.ToolRunResult{}, deliveryErr
		}
		break
	}
	if resp == nil {
		return app.ToolRunResult{}, deliveryErr
	}
	defer resp.Body.Close() //nolint:errcheck

	providerMsgID := firstNonEmptyString(
		resp.Header.Get("X-Request-Id"),
		resp.Header.Get("X-Slack-Req-Id"),
		resp.Header.Get("X-Correlation-Id"),
	)
	output := map[string]any{
		"delivered":       true,
		"channel":         route.Channel,
		"provider":        provider,
		"provider_msg_id": providerMsgID,
	}
	logMessage := fmt.Sprintf("notified %s via %s for incident %s", route.Channel, provider, request.IncidentID)
	return app.ToolRunResult{
		Output: output,
		Logs: []domain.LogLine{
			{At: h.now(), Channel: webKeyStdout, Message: logMessage},
		},
	}, nil
}

func (h *NotifyEscalationChannelHandler) allowDelivery(incidentID string) bool {
	now := h.now()
	incidentID = strings.TrimSpace(incidentID)

	h.mu.Lock()
	defer h.mu.Unlock()

	for id, last := range h.lastDelivery {
		if now.Sub(last) > notifyRateLimitWindow {
			delete(h.lastDelivery, id)
		}
	}
	if last, ok := h.lastDelivery[incidentID]; ok && now.Sub(last) < notifyRateLimitWindow {
		return false
	}
	h.lastDelivery[incidentID] = now
	return true
}

func loadNotifyEscalationRoutes(raw string) (map[string]NotifyEscalationRoute, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var routes map[string]NotifyEscalationRoute
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func toolRuntimeEnvironment(metadata map[string]string) string {
	if len(metadata) > 0 {
		if value := strings.ToLower(strings.TrimSpace(metadata["environment"])); value != "" {
			return value
		}
		if value := strings.ToLower(strings.TrimSpace(metadata["runtime_environment"])); value != "" && value != "unknown" {
			return value
		}
	}
	return strings.ToLower(strings.TrimSpace(os.Getenv("WORKSPACE_ENV")))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
