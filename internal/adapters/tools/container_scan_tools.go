package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type SecurityScanContainerHandler struct {
	runner app.CommandRunner
}

func NewSecurityScanContainerHandler(runner app.CommandRunner) *SecurityScanContainerHandler {
	return &SecurityScanContainerHandler{runner: runner}
}

func (h *SecurityScanContainerHandler) Name() string {
	return "security.scan_container"
}

func (h *SecurityScanContainerHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path              string `json:"path"`
		ImageRef          string `json:"image_ref"`
		MaxFindings       int    `json:"max_findings"`
		SeverityThreshold string `json:"severity_threshold"`
	}{
		Path:              ".",
		MaxFindings:       200,
		SeverityThreshold: sweSeverityMedium,
	}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid security.scan_container args",
			Retryable: false,
		}
	}

	scanPath, pathErr := sanitizeRelativePath(request.Path)
	if pathErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   pathErr.Error(),
			Retryable: false,
		}
	}
	if scanPath == "" {
		scanPath = "."
	}
	maxFindings := clampInt(request.MaxFindings, 1, 2000, 200)
	threshold, thresholdErr := normalizeSeverityThreshold(request.SeverityThreshold)
	if thresholdErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   thresholdErr.Error(),
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	imageRef := strings.TrimSpace(request.ImageRef)
	target := scanPath

	scanResult, scanDomErr := runContainerScan(ctx, runner, session, scanPath, imageRef, threshold, maxFindings)
	if scanDomErr != nil {
		return app.ToolRunResult{}, scanDomErr
	}
	command := scanResult.command
	commandResult := scanResult.commandResult
	runErr := scanResult.runErr
	findings := scanResult.findings
	truncated := scanResult.truncated
	scanner := scanResult.scanner
	rawOutput := scanResult.rawOutput
	if imageRef != "" {
		target = imageRef
	}

	severityCounts := intMapToAnyMap(countSecurityFindingsBySeverity(findings))
	result := app.ToolRunResult{
		ExitCode: commandResult.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: rawOutput}},
		Output: map[string]any{
			"scanner":            scanner,
			"target":             target,
			"path":               scanPath,
			"image_ref":          imageRef,
			"command":            command,
			"severity_threshold": threshold,
			"findings_count":     len(findings),
			"findings":           findings,
			"severity_counts":    severityCounts,
			"truncated":          truncated,
			"exit_code":          commandResult.ExitCode,
			"output":             rawOutput,
		},
		Artifacts: []app.ArtifactPayload{
			{
				Name:        sweArtifactContainerScanOutput,
				ContentType: sweTextPlain,
				Data:        []byte(rawOutput),
			},
		},
	}
	if findingsJSON, marshalErr := json.MarshalIndent(map[string]any{
		"scanner":            scanner,
		"target":             target,
		"severity_threshold": threshold,
		"findings_count":     len(findings),
		"findings":           findings,
		"severity_counts":    severityCounts,
		"truncated":          truncated,
	}, "", "  "); marshalErr == nil {
		result.Artifacts = append(result.Artifacts, app.ArtifactPayload{
			Name:        sweArtifactContainerScanFindings,
			ContentType: sweApplicationJSON,
			Data:        findingsJSON,
		})
	}
	if runErr != nil && len(findings) == 0 {
		return result, toToolError(runErr, rawOutput)
	}
	return result, nil
}

type containerScanResult struct {
	command       []string
	commandResult app.CommandResult
	runErr        error
	findings      []map[string]any
	truncated     bool
	scanner       string
	rawOutput     string
}

func runContainerScan(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	scanPath string,
	imageRef string,
	threshold string,
	maxFindings int,
) (containerScanResult, *domain.Error) {
	command, commandResult, runErr := runTrivyScan(ctx, runner, session, scanPath, imageRef, threshold)
	rawOutput := strings.TrimSpace(commandResult.Output)

	findings := []map[string]any{}
	truncated := false
	scanner := sweHeuristicDockerfile
	useHeuristicFallback := runErr != nil

	if !useHeuristicFallback {
		parsed, parsedTruncated, parseErr := parseTrivyFindings(commandResult.Output, threshold, maxFindings)
		if parseErr != nil {
			useHeuristicFallback = true
		} else {
			scanner = "trivy"
			findings = parsed
			truncated = parsedTruncated
		}
	}

	if useHeuristicFallback {
		hResult, hErr := applyHeuristicFallback(ctx, runner, session, scanPath, threshold, maxFindings, heuristicFallbackInput{existingOutput: rawOutput, existingCommand: command})
		if hErr != nil {
			return containerScanResult{}, hErr
		}
		rawOutput = hResult.rawOutput
		findings = hResult.findings
		truncated = hResult.truncated
		scanner = hResult.scanner
		command = hResult.command
		commandResult.ExitCode = 0
		runErr = nil
	}

	// If Trivy succeeds but produces zero findings on filesystem scans, run
	// Dockerfile heuristics as a deterministic secondary signal.
	if !useHeuristicFallback && imageRef == "" && len(findings) == 0 {
		heuristicFindings, heuristicTruncated, heuristicOutput, heuristicErr := scanContainerHeuristics(
			ctx, runner, session, scanPath, threshold, maxFindings,
		)
		if heuristicErr == nil && len(heuristicFindings) > 0 {
			rawOutput = mergeOutputStrings(rawOutput, heuristicOutput)
			findings = heuristicFindings
			truncated = heuristicTruncated
			scanner = sweHeuristicDockerfile
			command = []string{"heuristic", "dockerfile-scan", scanPath}
		}
	}

	return containerScanResult{
		command:       command,
		commandResult: commandResult,
		runErr:        runErr,
		findings:      findings,
		truncated:     truncated,
		scanner:       scanner,
		rawOutput:     rawOutput,
	}, nil
}

// runTrivyScan executes trivy against either an image reference or a
// filesystem path and returns the command slice, the raw result, and any error.
func runTrivyScan(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	scanPath, imageRef, threshold string,
) ([]string, app.CommandResult, error) {
	trivyArgs := []string{
		"--format", "json",
		"--quiet",
		"--no-progress",
		"--severity", strings.Join(severityListForThreshold(threshold), ","),
	}
	if imageRef != "" {
		command := append([]string{"trivy", "image"}, append(trivyArgs, imageRef)...)
		result, err := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "trivy",
			Args:     append([]string{"image"}, append(trivyArgs, imageRef)...),
			MaxBytes: 2 * 1024 * 1024,
		})
		return command, result, err
	}
	command := append([]string{"trivy", "fs"}, append(trivyArgs, scanPath)...)
	result, err := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "trivy",
		Args:     append([]string{"fs"}, append(trivyArgs, scanPath)...),
		MaxBytes: 2 * 1024 * 1024,
	})
	return command, result, err
}

type heuristicFallbackResult struct {
	command    []string
	findings   []map[string]any
	truncated  bool
	scanner    string
	rawOutput  string
}

// heuristicFallbackInput groups the prior scan state passed to
// applyHeuristicFallback.
type heuristicFallbackInput struct {
	existingOutput  string
	existingCommand []string
}

// applyHeuristicFallback runs the Dockerfile heuristic scanner as a fallback
// when Trivy is unavailable or fails to parse its output.
func applyHeuristicFallback(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	scanPath, threshold string,
	maxFindings int,
	prior heuristicFallbackInput,
) (heuristicFallbackResult, *domain.Error) {
	heuristicFindings, heuristicTruncated, heuristicOutput, heuristicErr := scanContainerHeuristics(
		ctx, runner, session, scanPath, threshold, maxFindings,
	)
	if heuristicErr != nil {
		return heuristicFallbackResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   heuristicErr.Error(),
			Retryable: false,
		}
	}
	cmd := prior.existingCommand
	if len(cmd) == 0 {
		cmd = []string{"heuristic", "dockerfile-scan", scanPath}
	}
	return heuristicFallbackResult{
		command:   cmd,
		findings:  heuristicFindings,
		truncated: heuristicTruncated,
		scanner:   sweHeuristicDockerfile,
		rawOutput: mergeOutputStrings(prior.existingOutput, heuristicOutput),
	}, nil
}

func mergeOutputStrings(existing, addition string) string {
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return existing + "\n\n" + addition
}

type trivyVulnerability struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	Description      string `json:"Description"`
	PrimaryURL       string `json:"PrimaryURL"`
}

type trivyMisconfiguration struct {
	ID          string `json:"ID"`
	AVDID       string `json:"AVDID"`
	Type        string `json:"Type"`
	Title       string `json:"Title"`
	Description string `json:"Description"`
	Message     string `json:"Message"`
	Resolution  string `json:"Resolution"`
	Severity    string `json:"Severity"`
}

type trivySecret struct {
	RuleID    string `json:"RuleID"`
	Category  string `json:"Category"`
	Title     string `json:"Title"`
	Severity  string `json:"Severity"`
	StartLine int    `json:"StartLine"`
	Match     string `json:"Match"`
}

type trivyResult struct {
	Target            string                  `json:"Target"`
	Class             string                  `json:"Class"`
	Type              string                  `json:"Type"`
	Vulnerabilities   []trivyVulnerability    `json:"Vulnerabilities"`
	Misconfigurations []trivyMisconfiguration `json:"Misconfigurations"`
	Secrets           []trivySecret           `json:"Secrets"`
}

type trivyReport struct {
	Results []trivyResult `json:"Results"`
}

func parseTrivyFindings(output, threshold string, maxFindings int) ([]map[string]any, bool, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, false, errors.New("empty trivy output")
	}

	report := trivyReport{}
	if err := json.Unmarshal([]byte(trimmed), &report); err != nil {
		var rawResults []trivyResult
		if json.Unmarshal([]byte(trimmed), &rawResults) != nil {
			return nil, false, err
		}
		report.Results = rawResults
	}

	findings := make([]map[string]any, 0, minInt(maxFindings, 256))
	for _, result := range report.Results {
		target := strings.TrimSpace(result.Target)
		if target == "" {
			target = "unknown"
		}
		findings = appendTrivyResultFindings(findings, result, target, threshold)
	}

	sort.Slice(findings, func(i, j int) bool {
		left := findings[i]
		right := findings[j]
		leftRank := securitySeverityRank(normalizeFindingSeverity(asString(left["severity"])))
		rightRank := securitySeverityRank(normalizeFindingSeverity(asString(right["severity"])))
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		leftTarget := asString(left["target"])
		rightTarget := asString(right["target"])
		if leftTarget != rightTarget {
			return leftTarget < rightTarget
		}
		leftID := asString(left["id"])
		rightID := asString(right["id"])
		if leftID != rightID {
			return leftID < rightID
		}
		return asString(left["kind"]) < asString(right["kind"])
	})

	truncated := false
	if len(findings) > maxFindings {
		findings = findings[:maxFindings]
		truncated = true
	}
	return findings, truncated, nil
}

func appendTrivyResultFindings(findings []map[string]any, result trivyResult, target, threshold string) []map[string]any {
	for _, vulnerability := range result.Vulnerabilities {
		severity := normalizeFindingSeverity(vulnerability.Severity)
		if !severityAtOrAbove(severity, threshold) {
			continue
		}
		id := nonEmptyOrDefault(strings.TrimSpace(vulnerability.VulnerabilityID), "unknown")
		findings = append(findings, map[string]any{
			"kind":              sweFindingVulnerability,
			"id":                id,
			"title":             nonEmptyOrDefault(strings.TrimSpace(vulnerability.Title), id),
			"severity":          severity,
			"target":            target,
			"package":           strings.TrimSpace(vulnerability.PkgName),
			"installed_version": nonEmptyOrDefault(strings.TrimSpace(vulnerability.InstalledVersion), sweUnknown),
			"fixed_version":     nonEmptyOrDefault(strings.TrimSpace(vulnerability.FixedVersion), sweUnknown),
			"description":       truncateString(strings.TrimSpace(vulnerability.Description), 400),
			"primary_url":       strings.TrimSpace(vulnerability.PrimaryURL),
		})
	}

	for _, misconfiguration := range result.Misconfigurations {
		severity := normalizeFindingSeverity(misconfiguration.Severity)
		if !severityAtOrAbove(severity, threshold) {
			continue
		}
		id := strings.TrimSpace(misconfiguration.ID)
		if id == "" {
			id = nonEmptyOrDefault(strings.TrimSpace(misconfiguration.AVDID), "unknown")
		}
		findings = append(findings, map[string]any{
			"kind":        sweFindingMisconfiguration,
			"id":          id,
			"title":       nonEmptyOrDefault(strings.TrimSpace(misconfiguration.Title), id),
			"severity":    severity,
			"target":      target,
			"type":        strings.TrimSpace(misconfiguration.Type),
			"description": truncateString(strings.TrimSpace(misconfiguration.Description), 400),
			"message":     truncateString(strings.TrimSpace(misconfiguration.Message), 240),
			"resolution":  truncateString(strings.TrimSpace(misconfiguration.Resolution), 240),
		})
	}

	for _, secret := range result.Secrets {
		severity := normalizeFindingSeverity(secret.Severity)
		if !severityAtOrAbove(severity, threshold) {
			continue
		}
		id := nonEmptyOrDefault(strings.TrimSpace(secret.RuleID), "unknown")
		findings = append(findings, map[string]any{
			"kind":     sweFindingSecret,
			"id":       id,
			"title":    nonEmptyOrDefault(strings.TrimSpace(secret.Title), id),
			"severity": severity,
			"target":   target,
			"category": strings.TrimSpace(secret.Category),
			"line":     secret.StartLine,
			"match":    truncateString(strings.TrimSpace(secret.Match), 180),
		})
	}
	return findings
}

func scanContainerHeuristics(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	scanPath string,
	threshold string,
	maxFindings int,
) ([]map[string]any, bool, string, error) {
	findResult, findErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "find",
		Args:     []string{scanPath, "-type", "f"},
		MaxBytes: 512 * 1024,
	})
	if findErr != nil {
		return nil, false, "", findErr
	}

	dockerfiles := make([]string, 0, 8)
	for _, candidate := range splitOutputLines(findResult.Output) {
		clean := strings.TrimSpace(candidate)
		if clean == "" {
			continue
		}
		if strings.Contains(clean, "/.git/") || strings.Contains(clean, "/node_modules/") || strings.Contains(clean, "/vendor/") || strings.Contains(clean, "/target/") || strings.Contains(clean, "/.workspace-venv/") {
			continue
		}
		if isDockerfileCandidate(filepath.Base(clean)) {
			dockerfiles = append(dockerfiles, clean)
		}
	}
	sort.Strings(dockerfiles)

	findings := make([]map[string]any, 0, minInt(maxFindings, 64))
	truncated := false
	for _, dockerfilePath := range dockerfiles {
		contentResult, contentErr := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "cat",
			Args:     []string{dockerfilePath},
			MaxBytes: 512 * 1024,
		})
		if contentErr != nil {
			continue
		}
		findings, truncated = scanDockerfileContent(findings, contentResult.Output, dockerfilePath, threshold, maxFindings)
		if truncated {
			break
		}
	}

	outputLines := []string{
		"heuristic container scan fallback executed",
		"scanner=heuristic-dockerfile",
		"dockerfiles_scanned=" + strconv.Itoa(len(dockerfiles)),
		"findings_count=" + strconv.Itoa(len(findings)),
		"truncated=" + strconv.FormatBool(truncated),
	}
	if len(dockerfiles) == 0 {
		outputLines = append(outputLines, "note=no Dockerfile found under requested path")
	}
	return findings, truncated, strings.Join(outputLines, "\n"), nil
}

func scanDockerfileContent(
	findings []map[string]any,
	content string,
	dockerfilePath string,
	threshold string,
	maxFindings int,
) ([]map[string]any, bool) {
	hasUser := false
	for index, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "user ") {
			hasUser = true
		}
		ruleID, severity, message := dockerfileHeuristicRule(lower)
		if ruleID == "" || !severityAtOrAbove(severity, threshold) {
			continue
		}
		findings = append(findings, map[string]any{
			"kind":     sweFindingMisconfiguration,
			"id":       ruleID,
			"title":    message,
			"severity": severity,
			"target":   filepath.ToSlash(dockerfilePath),
			"line":     index + 1,
		})
		if len(findings) >= maxFindings {
			return findings, true
		}
	}
	if !hasUser && severityAtOrAbove(sweSeverityMedium, threshold) {
		findings = append(findings, map[string]any{
			"kind":     sweFindingMisconfiguration,
			"id":       sweRuleMissingUser,
			"title":    sweRuleMsgMissingUser,
			"severity": sweSeverityMedium,
			"target":   filepath.ToSlash(dockerfilePath),
			"line":     0,
		})
		if len(findings) >= maxFindings {
			return findings, true
		}
	}
	return findings, false
}

func countSecurityFindingsBySeverity(findings []map[string]any) map[string]int {
	counts := map[string]int{
		sweSeverityCritical: 0,
		sweSeverityHigh:     0,
		sweSeverityMedium:   0,
		sweSeverityLow:      0,
		sweUnknown:              0,
	}
	for _, finding := range findings {
		severity := normalizeFindingSeverity(asString(finding["severity"]))
		counts[severity] = counts[severity] + 1
	}
	return counts
}

func dockerfileHeuristicRule(line string) (string, string, string) {
	switch {
	case strings.HasPrefix(line, "from "):
		if strings.Contains(line, "@sha256:") {
			return "", "", ""
		}
		if strings.Contains(line, ":latest") || !strings.Contains(line, ":") {
			return sweRuleUnpinnedBaseImage, sweSeverityMedium, sweRuleMsgUnpinnedBase
		}
	case strings.HasPrefix(line, "add "):
		return sweRuleAddInsteadOfCopy, sweSeverityLow, sweRuleMsgAddOverCopy
	case strings.HasPrefix(line, "run "):
		if (strings.Contains(line, "curl ") || strings.Contains(line, "wget ")) && strings.Contains(line, "|") {
			return sweRulePipeToShell, sweSeverityHigh, sweRuleMsgPipeToShell
		}
		if strings.Contains(line, "chmod 777") {
			return sweRuleChmod777, sweSeverityMedium, sweRuleMsgChmod777
		}
		if strings.Contains(line, "apt-get install") && !strings.Contains(line, "--no-install-recommends") {
			return sweRuleAptRecommends, sweSeverityLow, sweRuleMsgAptRecommends
		}
	}
	return "", "", ""
}

func isDockerfileCandidate(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if lower == "dockerfile" {
		return true
	}
	return strings.HasPrefix(lower, "dockerfile.") || strings.HasSuffix(lower, ".dockerfile")
}
