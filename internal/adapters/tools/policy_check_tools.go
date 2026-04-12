package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	policyCheckKeyStdout = "stdout"
)

// PolicyCheckHandler validates whether a tool invocation would be allowed
// by the policy engine WITHOUT actually executing it. Agents can pre-validate
// their intended actions to avoid wasted tool calls and policy denials.
type PolicyCheckHandler struct{}

func NewPolicyCheckHandler() *PolicyCheckHandler {
	return &PolicyCheckHandler{}
}

func (h *PolicyCheckHandler) Name() string {
	return "policy.check"
}

func (h *PolicyCheckHandler) Invoke(_ context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		ToolName string          `json:"tool_name"`
		Args     json.RawMessage `json:"args"`
	}{}

	if json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "invalid policy.check args", Retryable: false}
	}
	if strings.TrimSpace(request.ToolName) == "" {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "tool_name is required", Retryable: false}
	}

	caps := DefaultCapabilities()
	var cap *domain.Capability
	for i := range caps {
		if caps[i].Name == request.ToolName {
			cap = &caps[i]
			break
		}
	}
	if cap == nil {
		return policyCheckResult(request.ToolName, false, "tool not found in catalog"), nil
	}

	// Check: requires approval?
	if cap.RequiresApproval {
		// Not a denial, but worth noting.
	}

	// Check: risk level.
	riskNote := ""
	if cap.RiskLevel == domain.RiskHigh {
		riskNote = "high-risk tool — requires careful review"
	}

	// Validate args against policy.
	violations := policyCheckArgs(cap.Policy, request.Args, session)

	allowed := len(violations) == 0
	reason := "all policy checks passed"
	if !allowed {
		reason = strings.Join(violations, "; ")
	}

	output := map[string]any{
		"tool_name":         request.ToolName,
		"allowed":           allowed,
		"reason":            reason,
		"requires_approval": cap.RequiresApproval,
		"risk_level":        string(cap.RiskLevel),
		"side_effects":      string(cap.SideEffects),
	}
	if riskNote != "" {
		output["risk_note"] = riskNote
	}
	if len(violations) > 0 {
		output["violations"] = violations
	}

	return app.ToolRunResult{
		Output: output,
		Logs: []domain.LogLine{
			{At: time.Now().UTC(), Channel: policyCheckKeyStdout, Message: fmt.Sprintf("policy.check %s: allowed=%v", request.ToolName, allowed)},
		},
	}, nil
}

func policyCheckArgs(policy domain.PolicyMetadata, rawArgs json.RawMessage, session domain.Session) []string {
	if len(rawArgs) == 0 {
		return nil
	}

	var argMap map[string]any
	if json.Unmarshal(rawArgs, &argMap) != nil {
		return []string{"args is not a valid JSON object"}
	}

	var violations []string

	// Validate arg fields.
	for _, af := range policy.ArgFields {
		val, ok := argMap[af.Field]
		if !ok {
			continue
		}

		str, isStr := val.(string)
		if !isStr {
			continue
		}

		// Max length.
		if af.MaxLength > 0 && len(str) > af.MaxLength {
			violations = append(violations, fmt.Sprintf("%s exceeds max_length %d", af.Field, af.MaxLength))
		}

		// Deny characters.
		for _, ch := range af.DenyCharacters {
			if strings.Contains(str, ch) {
				violations = append(violations, fmt.Sprintf("%s contains denied character %q", af.Field, ch))
			}
		}

		// Denied prefix.
		for _, prefix := range af.DeniedPrefix {
			if strings.HasPrefix(str, prefix) {
				violations = append(violations, fmt.Sprintf("%s has denied prefix %q", af.Field, prefix))
			}
		}

		// Allowed prefix (if set, value must match at least one).
		if len(af.AllowedPrefix) > 0 {
			matched := false
			for _, prefix := range af.AllowedPrefix {
				if strings.HasPrefix(str, prefix) {
					matched = true
					break
				}
			}
			if !matched {
				violations = append(violations, fmt.Sprintf("%s does not match any allowed prefix", af.Field))
			}
		}
	}

	// Validate path fields.
	for _, pf := range policy.PathFields {
		val, ok := argMap[pf.Field]
		if !ok {
			continue
		}
		str, isStr := val.(string)
		if !isStr {
			continue
		}
		if pf.WorkspaceRelative {
			if _, pathErr := resolvePath(session, str); pathErr != nil {
				violations = append(violations, fmt.Sprintf("%s: %s", pf.Field, pathErr.Message))
			}
		}
	}

	return violations
}

func policyCheckResult(toolName string, allowed bool, reason string) app.ToolRunResult {
	return app.ToolRunResult{
		Output: map[string]any{
			"tool_name": toolName,
			"allowed":   allowed,
			"reason":    reason,
		},
		Logs: []domain.LogLine{
			{At: time.Now().UTC(), Channel: policyCheckKeyStdout, Message: fmt.Sprintf("policy.check %s: allowed=%v", toolName, allowed)},
		},
	}
}
