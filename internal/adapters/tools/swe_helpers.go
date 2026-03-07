package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	sweUnknown             = "unknown"
	sweTextPlain           = "text/plain"
	sweApplicationJSON     = "application/json"
	sweCoverageReportTxt   = "coverage-report.txt"
	sweCoverProfile        = "-coverprofile"
	sweCoverModeAtomic     = "-covermode=atomic"
	sweWorkspaceDist       = ".workspace-dist"
	sweHeuristicDockerfile = "heuristic-dockerfile"
	sweLicenseIsUnknown    = "license is unknown"
	sweCycloneDXJSON       = "cyclonedx-json"
	sweRgGlobFlag          = "--glob"
)

// Artifact names produced by tool handlers.
const (
	sweArtifactCIPipelineOutput      = "ci-pipeline-output.txt"
	sweArtifactQualityGateReport     = "quality-gate-report.json"
	sweArtifactLicenseCheckOutput    = "license-check-output.txt"
	sweArtifactLicenseCheckReport    = "license-check-report.json"
	sweArtifactContainerScanOutput   = "container-scan-output.txt"
	sweArtifactContainerScanFindings = "container-scan-findings.json"
	sweArtifactStaticAnalysisOutput  = "static-analysis-output.txt"
	sweArtifactPackageOutput         = "package-output.txt"
	sweArtifactDepScanOutput         = "dependency-scan-output.txt"
	sweArtifactDepInventory          = "dependency-inventory.json"
	sweArtifactSBOM                  = "sbom.cdx.json"
	sweArtifactSBOMOutput            = "sbom-generate-output.txt"
	sweArtifactSecretsScanOutput     = "secrets-scan-output.txt"
)

// PURL ecosystem prefixes (Package URL spec).
const (
	swePURLGolang  = "pkg:golang/"
	swePURLNPM     = "pkg:npm/"
	swePURLPyPI    = "pkg:pypi/"
	swePURLCargo   = "pkg:cargo/"
	swePURLMaven   = "pkg:maven/"
	swePURLGeneric = "pkg:generic/"
)

// Dockerfile heuristic rule identifiers.
const (
	sweRuleUnpinnedBaseImage  = "dockerfile.unpinned_base_image"
	sweRuleAddInsteadOfCopy   = "dockerfile.add_instead_of_copy"
	sweRulePipeToShell        = "dockerfile.pipe_to_shell"
	sweRuleChmod777           = "dockerfile.chmod_777"
	sweRuleAptRecommends      = "dockerfile.apt_install_recommends"
	sweRuleMissingUser        = "dockerfile.missing_user"
	sweRuleMsgUnpinnedBase    = "Base image should be pinned to a fixed version or digest."
	sweRuleMsgAddOverCopy     = "Prefer COPY over ADD unless archive extraction is required."
	sweRuleMsgPipeToShell     = "Avoid piping remote content directly into a shell."
	sweRuleMsgChmod777        = "Avoid world-writable permissions (chmod 777)."
	sweRuleMsgAptRecommends   = "Use --no-install-recommends to reduce image attack surface."
	sweRuleMsgMissingUser     = "Dockerfile does not define a non-root USER instruction."
)

// Quality gate rule names.
const (
	sweQGRuleCoverage        = "coverage_percent"
	sweQGRuleDiagnostics     = "diagnostics_count"
	sweQGRuleVulnerabilities = "vulnerabilities_count"
	sweQGRuleDeniedLicenses  = "denied_licenses_count"
	sweQGRuleFailedTests     = "failed_tests_count"
	sweQGOperatorGTE         = ">="
	sweQGOperatorLTE         = "<="
)

// Pipeline step statuses.
const (
	sweStepSucceeded = "succeeded"
	sweStepFailed    = "failed"
	sweStepSkipped   = "skipped"
)

// Pipeline step names.
const (
	sweStepValidate       = "validate"
	sweStepBuild          = "build"
	sweStepTest           = "test"
	sweStepStaticAnalysis = "static_analysis"
	sweStepCoverage       = "coverage"
	sweStepQualityGate    = "quality_gate"
)

// Verdict outcomes for license and quality gate evaluations.
const (
	sweVerdictPass = "pass"
	sweVerdictFail = "fail"
	sweVerdictWarn = "warn"
)

// License evaluation results.
const (
	sweLicenseAllowed = "allowed"
	sweLicenseDenied  = "denied"
	sweLicenseUnknown = "unknown"
)

// License policies for unknown licenses.
const (
	sweLicensePolicyWarn = "warn"
	sweLicensePolicyDeny = "deny"
)

// Severity levels for security findings.
const (
	sweSeverityCritical = "critical"
	sweSeverityHigh     = "high"
	sweSeverityMedium   = "medium"
	sweSeverityLow      = "low"
)

// Finding kinds in security scans.
const (
	sweFindingVulnerability    = "vulnerability"
	sweFindingMisconfiguration = "misconfiguration"
	sweFindingSecret           = "secret"
)

// Ecosystem identifiers.
const (
	sweEcosystemGo     = "go"
	sweEcosystemNode   = "node"
	sweEcosystemPython = "python"
	sweEcosystemRust   = "rust"
	sweEcosystemJava   = "java"
	sweEcosystemC      = "c"
)

func detectProjectTypeOrError(ctx context.Context, runner app.CommandRunner, session domain.Session, notFoundMsg string) (projectType, *domain.Error) {
	detected, err := detectProjectTypeForSession(ctx, runner, session)
	if err == nil {
		return detected, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return projectType{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: notFoundMsg, Retryable: false}
	}
	return projectType{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
}

// dependencyInventoryError maps a raw inventory error to a domain error,
// using notSupportedMsg when the underlying cause is os.ErrNotExist.
func dependencyInventoryError(err error, notSupportedMsg string) *domain.Error {
	if errors.Is(err, os.ErrNotExist) {
		return &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: notSupportedMsg, Retryable: false}
	}
	return &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
}

func isMissingBinaryError(runErr error, result app.CommandResult, command string) bool {
	if runErr == nil {
		return false
	}
	if result.ExitCode == 127 && strings.Contains(strings.ToLower(result.Output), "not found") {
		return true
	}
	errText := strings.ToLower(runErr.Error())
	return strings.Contains(errText, "not found") && strings.Contains(errText, strings.ToLower(command))
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		return int(typed)
	case uint64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
		if parsed, err := typed.Float64(); err == nil {
			return int(parsed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return 0
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case float64:
		return typed
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return parsed
		}
	}
	return 0
}

func normalizeSeverityThreshold(raw string) (string, error) {
	threshold := strings.ToLower(strings.TrimSpace(raw))
	switch threshold {
	case "", sweSeverityMedium, "moderate":
		return sweSeverityMedium, nil
	case sweSeverityLow:
		return sweSeverityLow, nil
	case sweSeverityHigh:
		return sweSeverityHigh, nil
	case sweSeverityCritical:
		return sweSeverityCritical, nil
	default:
		return "", errors.New("severity_threshold must be one of: low, medium, high, critical")
	}
}

func severityListForThreshold(threshold string) []string {
	switch normalizeFindingSeverity(threshold) {
	case sweSeverityCritical:
		return []string{"CRITICAL"}
	case sweSeverityHigh:
		return []string{"CRITICAL", "HIGH"}
	case sweSeverityMedium:
		return []string{"CRITICAL", "HIGH", "MEDIUM"}
	default:
		return []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"}
	}
}

func normalizeFindingSeverity(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case sweSeverityCritical:
		return sweSeverityCritical
	case sweSeverityHigh:
		return sweSeverityHigh
	case sweSeverityMedium, "moderate":
		return sweSeverityMedium
	case sweSeverityLow:
		return sweSeverityLow
	default:
		return sweUnknown
	}
}

func severityAtOrAbove(severity, threshold string) bool {
	return securitySeverityRank(normalizeFindingSeverity(severity)) >= securitySeverityRank(normalizeFindingSeverity(threshold))
}

func securitySeverityRank(severity string) int {
	switch normalizeFindingSeverity(severity) {
	case sweSeverityCritical:
		return 4
	case sweSeverityHigh:
		return 3
	case sweSeverityMedium:
		return 2
	case sweSeverityLow:
		return 1
	default:
		return 0
	}
}

func asString(raw any) string {
	value, _ := raw.(string)
	return strings.TrimSpace(value)
}

func intMapToAnyMap(raw map[string]int) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func truncateString(raw string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(raw) <= maxLen {
		return raw
	}
	return raw[:maxLen]
}

func nonEmptyOrDefault(raw, fallback string) string {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return raw
}

func targetOrDefault(target, fallback string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
