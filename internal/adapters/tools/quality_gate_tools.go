package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type QualityGateHandler struct {
	runner app.CommandRunner
}

func NewQualityGateHandler(runner app.CommandRunner) *QualityGateHandler {
	return &QualityGateHandler{runner: runner}
}

type qualityGateThresholdsRequest struct {
	MinCoveragePercent *float64 `json:"min_coverage_percent"`
	MaxDiagnostics     *int     `json:"max_diagnostics"`
	MaxVulnerabilities *int     `json:"max_vulnerabilities"`
	MaxDeniedLicenses  *int     `json:"max_denied_licenses"`
	MaxFailedTests     *int     `json:"max_failed_tests"`
}

type qualityGateRequest struct {
	Metrics map[string]any `json:"metrics"`
	qualityGateThresholdsRequest
}

type qualityGateConfig struct {
	MinCoveragePercent float64
	MaxDiagnostics     int
	MaxVulnerabilities int
	MaxDeniedLicenses  int
	MaxFailedTests     int
}

type qualityGateMetrics struct {
	CoveragePercent      float64 `json:"coverage_percent"`
	DiagnosticsCount     int     `json:"diagnostics_count"`
	VulnerabilitiesCount int     `json:"vulnerabilities_count"`
	DeniedLicensesCount  int     `json:"denied_licenses_count"`
	FailedTestsCount     int     `json:"failed_tests_count"`
}

type qualityGateRule struct {
	Name     string  `json:"name"`
	Operator string  `json:"operator"`
	Expected float64 `json:"expected"`
	Actual   float64 `json:"actual"`
	Passed   bool    `json:"passed"`
	Message  string  `json:"message"`
}

func (h *QualityGateHandler) Name() string {
	return "quality.gate"
}

func (h *QualityGateHandler) Invoke(_ context.Context, _ domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := qualityGateRequest{}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid quality.gate args",
			Retryable: false,
		}
	}

	metrics := qualityGateMetricsFromMap(request.Metrics)
	config := normalizeQualityGateConfig(request.qualityGateThresholdsRequest)
	rules, passed := evaluateQualityGate(metrics, config)
	failedRules := countFailedQualityRules(rules)
	status := sweVerdictPass
	exitCode := 0
	if !passed {
		status = sweVerdictFail
		exitCode = 1
	}
	summary := qualityGateSummary(passed, len(rules)-failedRules, len(rules))

	output := map[string]any{
		"status":             status,
		"passed":             passed,
		"failed_rules_count": failedRules,
		"rules":              rules,
		"metrics":            qualityGateMetricsToMap(metrics),
		"thresholds":         qualityGateConfigToMap(config),
		"summary":            summary,
		"output":             summary,
	}

	result := app.ToolRunResult{
		ExitCode:  exitCode,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: summary}},
		Output:    output,
		Artifacts: []app.ArtifactPayload{},
	}
	if reportBytes, marshalErr := json.MarshalIndent(output, "", "  "); marshalErr == nil {
		result.Artifacts = append(result.Artifacts, app.ArtifactPayload{
			Name:        sweArtifactQualityGateReport,
			ContentType: sweApplicationJSON,
			Data:        reportBytes,
		})
	}
	return result, nil
}

func qualityGateMetricsFromMap(raw map[string]any) qualityGateMetrics {
	if len(raw) == 0 {
		return qualityGateMetrics{}
	}
	metrics := qualityGateMetrics{
		CoveragePercent:      floatFromAny(raw[sweQGRuleCoverage]),
		DiagnosticsCount:     intFromAny(raw[sweQGRuleDiagnostics]),
		VulnerabilitiesCount: intFromAny(raw[sweQGRuleVulnerabilities]),
		DeniedLicensesCount:  intFromAny(raw[sweQGRuleDeniedLicenses]),
		FailedTestsCount:     intFromAny(raw[sweQGRuleFailedTests]),
	}
	if metrics.VulnerabilitiesCount == 0 {
		metrics.VulnerabilitiesCount = intFromAny(raw["vulns_count"])
	}
	if metrics.DeniedLicensesCount == 0 {
		metrics.DeniedLicensesCount = intFromAny(raw["denied_count"])
	}
	return metrics
}

func normalizeQualityGateConfig(request qualityGateThresholdsRequest) qualityGateConfig {
	config := qualityGateConfig{
		MinCoveragePercent: -1,
		MaxDiagnostics:     -1,
		MaxVulnerabilities: -1,
		MaxDeniedLicenses:  -1,
		MaxFailedTests:     0,
	}
	if request.MinCoveragePercent != nil {
		value := *request.MinCoveragePercent
		switch {
		case value < 0:
			config.MinCoveragePercent = -1
		case value > 100:
			config.MinCoveragePercent = 100
		default:
			config.MinCoveragePercent = value
		}
	}
	if request.MaxDiagnostics != nil {
		config.MaxDiagnostics = clampMaxThreshold(*request.MaxDiagnostics)
	}
	if request.MaxVulnerabilities != nil {
		config.MaxVulnerabilities = clampMaxThreshold(*request.MaxVulnerabilities)
	}
	if request.MaxDeniedLicenses != nil {
		config.MaxDeniedLicenses = clampMaxThreshold(*request.MaxDeniedLicenses)
	}
	if request.MaxFailedTests != nil {
		config.MaxFailedTests = clampMaxThreshold(*request.MaxFailedTests)
	}
	return config
}

func clampMaxThreshold(value int) int {
	if value < 0 {
		return -1
	}
	if value > 100000 {
		return 100000
	}
	return value
}

func evaluateQualityGate(metrics qualityGateMetrics, config qualityGateConfig) ([]qualityGateRule, bool) {
	rules := make([]qualityGateRule, 0, 5)

	if config.MinCoveragePercent >= 0 {
		passed := metrics.CoveragePercent >= config.MinCoveragePercent
		rules = append(rules, qualityGateRule{
			Name:     sweQGRuleCoverage,
			Operator: sweQGOperatorGTE,
			Expected: config.MinCoveragePercent,
			Actual:   metrics.CoveragePercent,
			Passed:   passed,
			Message:  fmt.Sprintf("coverage %.2f%% >= %.2f%%", metrics.CoveragePercent, config.MinCoveragePercent),
		})
	}

	appendMaxRule := func(name string, actual int, max int) {
		if max < 0 {
			return
		}
		passed := actual <= max
		rules = append(rules, qualityGateRule{
			Name:     name,
			Operator: sweQGOperatorLTE,
			Expected: float64(max),
			Actual:   float64(actual),
			Passed:   passed,
			Message:  fmt.Sprintf("%s %d <= %d", name, actual, max),
		})
	}

	appendMaxRule(sweQGRuleDiagnostics, metrics.DiagnosticsCount, config.MaxDiagnostics)
	appendMaxRule(sweQGRuleVulnerabilities, metrics.VulnerabilitiesCount, config.MaxVulnerabilities)
	appendMaxRule(sweQGRuleDeniedLicenses, metrics.DeniedLicensesCount, config.MaxDeniedLicenses)
	appendMaxRule(sweQGRuleFailedTests, metrics.FailedTestsCount, config.MaxFailedTests)

	passed := true
	for _, rule := range rules {
		if !rule.Passed {
			passed = false
			break
		}
	}
	return rules, passed
}

func countFailedQualityRules(rules []qualityGateRule) int {
	failed := 0
	for _, rule := range rules {
		if !rule.Passed {
			failed++
		}
	}
	return failed
}

func qualityGateSummary(passed bool, passedRules int, totalRules int) string {
	if totalRules <= 0 {
		if passed {
			return "quality gate passed (no active rules)"
		}
		return "quality gate failed (no active rules)"
	}
	if passed {
		return fmt.Sprintf("quality gate passed (%d/%d rules)", passedRules, totalRules)
	}
	return fmt.Sprintf("quality gate failed (%d/%d rules)", passedRules, totalRules)
}

func qualityGateConfigToMap(config qualityGateConfig) map[string]any {
	return map[string]any{
		"min_coverage_percent": config.MinCoveragePercent,
		"max_diagnostics":      config.MaxDiagnostics,
		"max_vulnerabilities":  config.MaxVulnerabilities,
		"max_denied_licenses":  config.MaxDeniedLicenses,
		"max_failed_tests":     config.MaxFailedTests,
	}
}

func qualityGateMetricsToMap(metrics qualityGateMetrics) map[string]any {
	return map[string]any{
		sweQGRuleCoverage:        metrics.CoveragePercent,
		sweQGRuleDiagnostics:     metrics.DiagnosticsCount,
		sweQGRuleVulnerabilities: metrics.VulnerabilitiesCount,
		sweQGRuleDeniedLicenses:  metrics.DeniedLicensesCount,
		sweQGRuleFailedTests:     metrics.FailedTestsCount,
	}
}

func ternaryQualityGateStatus(passed bool) string {
	if passed {
		return sweVerdictPass
	}
	return sweVerdictFail
}
