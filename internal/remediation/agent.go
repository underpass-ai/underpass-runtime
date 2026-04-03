package remediation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// RuntimeClient is the interface to the workspace runtime gRPC service.
type RuntimeClient interface {
	CreateSession(ctx context.Context, tenantID, actorID string) (string, error)
	CloseSession(ctx context.Context, sessionID string) error
	RecommendTools(ctx context.Context, sessionID, taskHint string, topK int) ([]string, string, error) // returns (toolNames, recommendationID, error)
	InvokeTool(ctx context.Context, sessionID, toolName string) (InvokeResult, error)
	AcceptRecommendation(ctx context.Context, sessionID, recID, toolID string) error
	RejectRecommendation(ctx context.Context, sessionID, recID, reason string) error
}

// InvokeResult captures the outcome of a tool invocation.
type InvokeResult struct {
	Status     string
	DurationMS int64
	Error      string
}

// AlertEvent mirrors the alert-relay event schema.
type AlertEvent struct {
	ID        string             `json:"id"`
	Type      string             `json:"type"`
	AlertName string             `json:"alert_name"`
	Status    string             `json:"status"`
	Severity  string             `json:"severity"`
	Summary   string             `json:"summary"`
	Labels    map[string]string  `json:"labels,omitempty"`
	Values    map[string]float64 `json:"values,omitempty"`
}

// RunResult captures the outcome of a remediation run.
type RunResult struct {
	AlertName    string        `json:"alert_name"`
	PlaybookDesc string        `json:"playbook"`
	SessionID    string        `json:"session_id"`
	Steps        []StepResult  `json:"steps"`
	Duration     time.Duration `json:"duration_ms"`
	Outcome      string        `json:"outcome"` // success, partial, failed
}

// StepResult captures one step in a remediation run.
type StepResult struct {
	Tool     string `json:"tool"`
	Status   string `json:"status"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
}

// Agent runs remediation playbooks in response to alert events.
type Agent struct {
	client    RuntimeClient
	playbooks []Playbook
	tenantID  string
	actorID   string
	logger    *slog.Logger
}

// AgentConfig configures the remediation agent.
type AgentConfig struct {
	Client    RuntimeClient
	Playbooks []Playbook
	TenantID  string
	ActorID   string
	Logger    *slog.Logger
}

// NewAgent creates a remediation agent.
func NewAgent(cfg AgentConfig) *Agent {
	playbooks := cfg.Playbooks
	if len(playbooks) == 0 {
		playbooks = DefaultPlaybooks()
	}
	return &Agent{
		client:    cfg.Client,
		playbooks: playbooks,
		tenantID:  cfg.TenantID,
		actorID:   cfg.ActorID,
		logger:    cfg.Logger,
	}
}

// HandleAlert processes a raw NATS message containing an AlertEvent.
func (a *Agent) HandleAlert(ctx context.Context, data []byte) (*RunResult, error) {
	var alert AlertEvent
	if err := json.Unmarshal(data, &alert); err != nil {
		return nil, fmt.Errorf("unmarshal alert: %w", err)
	}

	if alert.Status != "firing" {
		a.logger.Info("skipping non-firing alert", "alert", alert.AlertName, "status", alert.Status)
		return nil, nil
	}

	pb, found := MatchPlaybook(a.playbooks, alert.AlertName)
	if !found {
		a.logger.Info("no playbook for alert", "alert", alert.AlertName)
		return nil, nil
	}

	a.logger.Info("remediation started",
		"alert", alert.AlertName,
		"severity", alert.Severity,
		"playbook", pb.Description,
	)

	return a.executePlaybook(ctx, alert, pb)
}

func (a *Agent) executePlaybook(ctx context.Context, alert AlertEvent, pb Playbook) (*RunResult, error) {
	start := time.Now()
	result := &RunResult{
		AlertName:    alert.AlertName,
		PlaybookDesc: pb.Description,
	}

	// 1. Create session
	sessionID, err := a.client.CreateSession(ctx, a.tenantID, a.actorID)
	if err != nil {
		result.Outcome = "failed"
		return result, fmt.Errorf("create session: %w", err)
	}
	result.SessionID = sessionID
	defer func() {
		_ = a.client.CloseSession(ctx, sessionID)
	}()

	// 2. Get recommendations
	recTools, recID, err := a.client.RecommendTools(ctx, sessionID, pb.DiagnoseHint, 5)
	if err != nil {
		result.Outcome = "failed"
		return result, fmt.Errorf("recommend tools: %w", err)
	}

	a.logger.Info("recommendations received",
		"session", sessionID,
		"recommendation_id", recID,
		"tools", recTools,
	)

	// 3. Execute playbook tools
	tools := pb.Tools
	if len(tools) == 0 && len(recTools) > 0 {
		tools = recTools[:1] // use top recommendation if no explicit tools
	}

	allSucceeded := true
	usedRecommended := false
	for _, tool := range tools {
		invokeResult, invokeErr := a.client.InvokeTool(ctx, sessionID, tool)
		step := StepResult{
			Tool:     tool,
			Status:   invokeResult.Status,
			Duration: invokeResult.DurationMS,
		}
		if invokeErr != nil {
			step.Status = "error"
			step.Error = invokeErr.Error()
			allSucceeded = false
		} else if invokeResult.Status != "INVOCATION_STATUS_SUCCEEDED" {
			allSucceeded = false
		}
		result.Steps = append(result.Steps, step)

		// Check if this tool was recommended
		for _, rt := range recTools {
			if rt == tool {
				usedRecommended = true
			}
		}
	}

	// 4. Report feedback
	if usedRecommended && recID != "" {
		if allSucceeded {
			if err := a.client.AcceptRecommendation(ctx, sessionID, recID, tools[0]); err != nil {
				a.logger.Warn("accept recommendation failed", "recommendation_id", recID, "error", err)
			}
		} else {
			if err := a.client.RejectRecommendation(ctx, sessionID, recID, "remediation partially failed"); err != nil {
				a.logger.Warn("reject recommendation failed", "recommendation_id", recID, "error", err)
			}
		}
	}

	result.Duration = time.Since(start)
	switch {
	case allSucceeded:
		result.Outcome = "success"
	case len(result.Steps) > 0:
		result.Outcome = "partial"
	default:
		result.Outcome = "failed"
	}

	a.logger.Info("remediation completed",
		"alert", alert.AlertName,
		"session", sessionID,
		"outcome", result.Outcome,
		"steps", len(result.Steps),
		"duration_ms", result.Duration.Milliseconds(),
	)

	return result, nil
}
