package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type SecurityLicenseCheckHandler struct {
	runner app.CommandRunner
}

func NewSecurityLicenseCheckHandler(runner app.CommandRunner) *SecurityLicenseCheckHandler {
	return &SecurityLicenseCheckHandler{runner: runner}
}

func (h *SecurityLicenseCheckHandler) Name() string {
	return "security.license_check"
}

// licenseCheckParams holds the validated parameters for a license check invocation.
type licenseCheckParams struct {
	scanPath        string
	maxDependencies int
	unknownPolicy   string
	allowedLicenses []string
	deniedLicenses  []string
}

// parseLicenseCheckRequest unmarshals and validates the license check args.
func parseLicenseCheckRequest(args json.RawMessage) (licenseCheckParams, *domain.Error) {
	request := struct {
		Path            string   `json:"path"`
		MaxDependencies int      `json:"max_dependencies"`
		AllowedLicenses []string `json:"allowed_licenses"`
		DeniedLicenses  []string `json:"denied_licenses"`
		UnknownPolicy   string   `json:"unknown_policy"`
	}{
		Path:            ".",
		MaxDependencies: 500,
		UnknownPolicy:   sweLicensePolicyWarn,
	}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return licenseCheckParams{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid security.license_check args",
			Retryable: false,
		}
	}
	scanPath, pathErr := sanitizeRelativePath(request.Path)
	if pathErr != nil {
		return licenseCheckParams{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   pathErr.Error(),
			Retryable: false,
		}
	}
	if scanPath == "" {
		scanPath = "."
	}
	unknownPolicy := strings.ToLower(strings.TrimSpace(request.UnknownPolicy))
	if unknownPolicy == "" {
		unknownPolicy = sweLicensePolicyWarn
	}
	if unknownPolicy != sweLicensePolicyWarn && unknownPolicy != sweLicensePolicyDeny {
		return licenseCheckParams{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "unknown_policy must be warn or deny",
			Retryable: false,
		}
	}
	return licenseCheckParams{
		scanPath:        scanPath,
		maxDependencies: clampInt(request.MaxDependencies, 1, 5000, 500),
		unknownPolicy:   unknownPolicy,
		allowedLicenses: normalizeLicensePolicyTokens(request.AllowedLicenses),
		deniedLicenses:  normalizeLicensePolicyTokens(request.DeniedLicenses),
	}, nil
}

func (h *SecurityLicenseCheckHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	params, parseErr := parseLicenseCheckRequest(args)
	if parseErr != nil {
		return app.ToolRunResult{}, parseErr
	}
	scanPath := params.scanPath
	maxDependencies := params.maxDependencies
	unknownPolicy := params.unknownPolicy
	allowedLicenses := params.allowedLicenses
	deniedLicenses := params.deniedLicenses

	runner := ensureRunner(h.runner)
	detected, detectDomErr := detectProjectTypeOrError(ctx, runner, session, "no supported license check toolchain found")
	if detectDomErr != nil {
		return app.ToolRunResult{}, detectDomErr
	}

	inventory, inventoryErr := collectDependencyInventory(ctx, runner, session, detected, scanPath, maxDependencies)
	if inventoryErr != nil {
		return app.ToolRunResult{}, dependencyInventoryError(inventoryErr, "license check is not supported for detected project type")
	}

	enrichedEntries, enrichmentCommand, enrichmentOutput, enrichmentErr := enrichDependencyLicenses(ctx, runner, session, licenseEnrichmentInput{
		detected:        detected,
		scanPath:        scanPath,
		entries:         inventory.Dependencies,
		maxDependencies: maxDependencies,
	})
	if enrichmentErr != nil && len(enrichedEntries) == 0 {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   enrichmentErr.Error(),
			Retryable: false,
		}
	}
	if len(enrichedEntries) == 0 {
		enrichedEntries = inventory.Dependencies
	}

	classification := classifyLicenseEntries(enrichedEntries, allowedLicenses, deniedLicenses, unknownPolicy)

	outputParts := make([]string, 0, 2)
	if s := strings.TrimSpace(inventory.Output); s != "" {
		outputParts = append(outputParts, s)
	}
	if s := strings.TrimSpace(enrichmentOutput); s != "" {
		outputParts = append(outputParts, s)
	}
	combinedOutput := strings.Join(outputParts, "\n\n")

	result := app.ToolRunResult{
		ExitCode: classification.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: combinedOutput}},
		Output: map[string]any{
			"project_type":           detected.Name,
			"command":                inventory.Command,
			"license_source_command": enrichmentCommand,
			"dependencies_checked":   len(enrichedEntries),
			"allowed_count":          classification.AllowedCount,
			"denied_count":           classification.DeniedCount,
			"unknown_count":          classification.UnknownCount,
			"allowed_licenses":       allowedLicenses,
			"denied_licenses":        deniedLicenses,
			"unknown_policy":         unknownPolicy,
			"status":                 classification.Status,
			"violations":             classification.Violations,
			"dependencies":           dependencyEntriesToMaps(enrichedEntries),
			"truncated":              inventory.Truncated,
			"exit_code":              classification.ExitCode,
			"output":                 combinedOutput,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweArtifactLicenseCheckOutput,
			ContentType: sweTextPlain,
			Data:        []byte(combinedOutput),
		}},
	}
	if reportBytes, marshalErr := json.MarshalIndent(map[string]any{
		"project_type":         detected.Name,
		"dependencies_checked": len(enrichedEntries),
		"allowed_count":        classification.AllowedCount,
		"denied_count":         classification.DeniedCount,
		"unknown_count":        classification.UnknownCount,
		"status":               classification.Status,
		"violations":           classification.Violations,
		"dependencies":         dependencyEntriesToMaps(enrichedEntries),
		"truncated":            inventory.Truncated,
	}, "", "  "); marshalErr == nil {
		result.Artifacts = append(result.Artifacts, app.ArtifactPayload{
			Name:        sweArtifactLicenseCheckReport,
			ContentType: sweApplicationJSON,
			Data:        reportBytes,
		})
	}

	if inventory.RunErr != nil && len(enrichedEntries) == 0 {
		return result, toToolError(inventory.RunErr, combinedOutput)
	}
	return result, nil
}

// licenseClassification is a value object that carries the result of classifying
// dependency licenses against a policy, including the derived verdict.
type licenseClassification struct {
	Violations   []map[string]any
	AllowedCount int
	DeniedCount  int
	UnknownCount int
	Status       string
	ExitCode     int
}

func classifyLicenseEntries(
	entries []dependencyEntry,
	allowedLicenses []string,
	deniedLicenses []string,
	unknownPolicy string,
) licenseClassification {
	c := licenseClassification{Violations: make([]map[string]any, 0, 32)}
	for _, entry := range entries {
		license := strings.TrimSpace(entry.License)
		if license == "" {
			license = sweLicenseUnknown
		}
		licenseStatus, reason := evaluateLicenseAgainstPolicy(license, allowedLicenses, deniedLicenses)
		if licenseStatus == sweLicenseUnknown {
			c.UnknownCount++
			if unknownPolicy == sweLicensePolicyDeny {
				c.Violations = append(c.Violations, map[string]any{
					"name":      entry.Name,
					"version":   entry.Version,
					"ecosystem": entry.Ecosystem,
					"license":   license,
					"reason":    "unknown license is denied by policy",
				})
			}
			continue
		}
		if licenseStatus == sweLicenseDenied {
			c.DeniedCount++
			c.Violations = append(c.Violations, map[string]any{
				"name":      entry.Name,
				"version":   entry.Version,
				"ecosystem": entry.Ecosystem,
				"license":   license,
				"reason":    reason,
			})
			continue
		}
		c.AllowedCount++
	}
	c.Status = sweVerdictPass
	if c.DeniedCount > 0 || (unknownPolicy == sweLicensePolicyDeny && c.UnknownCount > 0) {
		c.Status = sweVerdictFail
		c.ExitCode = 1
	} else if c.UnknownCount > 0 {
		c.Status = sweVerdictWarn
	}
	return c
}

func normalizeLicensePolicyTokens(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, 0, len(tokens))
	seen := map[string]struct{}{}
	for _, token := range tokens {
		for _, part := range splitPolicyTokens(token) {
			normalized := normalizeLicenseToken(part)
			if normalized == "" {
				continue
			}
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

// licenseEnrichmentInput groups the parameters for enriching dependency
// license information.
type licenseEnrichmentInput struct {
	detected        projectType
	scanPath        string
	entries         []dependencyEntry
	maxDependencies int
}

func enrichDependencyLicenses(ctx context.Context, runner app.CommandRunner, session domain.Session, in licenseEnrichmentInput) ([]dependencyEntry, []string, string, error) {
	enriched := cloneDependencyEntries(in.entries)
	if len(enriched) == 0 {
		return enriched, nil, "", nil
	}
	for i := range enriched {
		enriched[i].License = nonEmptyOrDefault(strings.TrimSpace(enriched[i].License), sweLicenseUnknown)
	}

	cwd := session.WorkspacePath
	if in.scanPath != "" && in.scanPath != "." {
		cwd = filepath.Join(session.WorkspacePath, filepath.FromSlash(in.scanPath))
	}

	switch in.detected.Name {
	case sweEcosystemNode:
		command := []string{"npm", "ls", "--json", "--all", "--long"}
		result, runErr := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      cwd,
			Command:  command[0],
			Args:     command[1:],
			MaxBytes: 2 * 1024 * 1024,
		})
		licenseByDependency, parseErr := parseNodeLicenseMap(result.Output, in.maxDependencies)
		if parseErr != nil && runErr == nil {
			return enriched, command, result.Output, parseErr
		}
		applyDependencyLicenses(enriched, licenseByDependency)
		return enriched, command, result.Output, runErr
	case sweEcosystemRust:
		command := []string{"cargo", "metadata", "--format-version", "1"}
		result, runErr := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      cwd,
			Command:  command[0],
			Args:     command[1:],
			MaxBytes: 2 * 1024 * 1024,
		})
		licenseByDependency, parseErr := parseRustLicenseMap(result.Output, in.maxDependencies)
		if parseErr != nil && runErr == nil {
			return enriched, command, result.Output, parseErr
		}
		applyDependencyLicenses(enriched, licenseByDependency)
		return enriched, command, result.Output, runErr
	default:
		return enriched, nil, "", nil
	}
}

func evaluateLicenseAgainstPolicy(license string, allowedLicenses []string, deniedLicenses []string) (string, string) {
	tokens := licenseExpressionTokens(license)
	if len(tokens) == 0 {
		return sweLicenseUnknown, sweLicenseIsUnknown
	}
	allowedSet := sliceToStringSet(allowedLicenses)
	deniedSet := sliceToStringSet(deniedLicenses)

	unknown, deniedStatus, deniedReason := checkTokensAgainstDenied(tokens, deniedSet)
	if deniedStatus != "" {
		return deniedStatus, deniedReason
	}

	if len(allowedSet) > 0 {
		return checkTokensAgainstAllowed(tokens, allowedSet, unknown)
	}

	if unknown {
		return sweLicenseUnknown, sweLicenseIsUnknown
	}
	return sweLicenseAllowed, ""
}

func sliceToStringSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}

func checkTokensAgainstDenied(tokens []string, deniedSet map[string]struct{}) (unknown bool, status, reason string) {
	for _, token := range tokens {
		if token == "UNKNOWN" {
			unknown = true
			continue
		}
		if _, denied := deniedSet[token]; denied {
			return unknown, sweLicenseDenied, "matched denied license: " + token
		}
	}
	return unknown, "", ""
}

func checkTokensAgainstAllowed(tokens []string, allowedSet map[string]struct{}, unknown bool) (string, string) {
	for _, token := range tokens {
		if _, allowed := allowedSet[token]; allowed {
			return sweLicenseAllowed, ""
		}
	}
	if unknown {
		return sweLicenseUnknown, sweLicenseIsUnknown
	}
	return sweLicenseDenied, "license not present in allowed_licenses"
}

func parseNodeLicenseMap(output string, maxDependencies int) (map[string]string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, err
	}
	dependencies, _ := payload["dependencies"].(map[string]any)
	licenseByDependency := make(map[string]string, minInt(len(dependencies), maxDependencies))
	seen := map[string]struct{}{}
	walkNodeLicenseMap(dependencies, maxDependencies, seen, licenseByDependency)
	return licenseByDependency, nil
}

func walkNodeLicenseMap(tree map[string]any, maxDependencies int, seen map[string]struct{}, out map[string]string) {
	if tree == nil {
		return
	}
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if len(out) >= maxDependencies {
			return
		}
		rawNode, ok := tree[name]
		if !ok {
			continue
		}
		node, ok := rawNode.(map[string]any)
		if !ok {
			continue
		}
		version := nonEmptyOrDefault(strings.TrimSpace(asString(node["version"])), sweUnknown)
		key := dependencyLicenseLookupKey(sweEcosystemNode, name, version)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			license := normalizeFoundLicense(nodeStringOrList(node["license"]))
			if license != "" && license != sweLicenseUnknown {
				out[key] = license
			}
		}

		children, _ := node["dependencies"].(map[string]any)
		walkNodeLicenseMap(children, maxDependencies, seen, out)
	}
}

func parseRustLicenseMap(output string, maxDependencies int) (map[string]string, error) {
	type rustMetadata struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			License string `json:"license"`
		} `json:"packages"`
	}

	payload := rustMetadata{}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, err
	}
	out := make(map[string]string, minInt(len(payload.Packages), maxDependencies))
	for _, pkg := range payload.Packages {
		if len(out) >= maxDependencies {
			break
		}
		name := strings.TrimSpace(pkg.Name)
		version := nonEmptyOrDefault(strings.TrimSpace(pkg.Version), sweUnknown)
		if name == "" {
			continue
		}
		license := normalizeFoundLicense(pkg.License)
		if license == "" || license == sweLicenseUnknown {
			continue
		}
		out[dependencyLicenseLookupKey(sweEcosystemRust, name, version)] = license
	}
	return out, nil
}

func applyDependencyLicenses(entries []dependencyEntry, licenseByDependency map[string]string) {
	if len(entries) == 0 || len(licenseByDependency) == 0 {
		return
	}
	for index := range entries {
		key := dependencyLicenseLookupKey(entries[index].Ecosystem, entries[index].Name, entries[index].Version)
		license, exists := licenseByDependency[key]
		if !exists {
			continue
		}
		entries[index].License = nonEmptyOrDefault(license, entries[index].License)
	}
}

func dependencyLicenseLookupKey(ecosystem, name, version string) string {
	normalizedEcosystem := strings.TrimSpace(strings.ToLower(ecosystem))
	normalizedName := strings.TrimSpace(name)
	switch normalizedEcosystem {
	case sweEcosystemNode, sweEcosystemPython, sweEcosystemRust:
		normalizedName = strings.ToLower(normalizedName)
	}
	return normalizedEcosystem + "|" + normalizedName + "|" + nonEmptyOrDefault(strings.TrimSpace(version), sweUnknown)
}

func splitPolicyTokens(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';':
			return true
		default:
			return unicode.IsSpace(r)
		}
	})
}

func normalizeLicenseToken(raw string) string {
	token := strings.ToUpper(strings.TrimSpace(raw))
	token = strings.Trim(token, "()")
	token = strings.ReplaceAll(token, "_", "-")
	switch token {
	case "", "N/A", "NONE", "NOASSERTION":
		return "UNKNOWN"
	default:
		return token
	}
}

func licenseExpressionTokens(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.FieldsFunc(strings.ToUpper(trimmed), func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		switch r {
		case '.', '-', '+':
			return false
		default:
			return true
		}
	})
	tokens := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		token := normalizeLicenseToken(part)
		switch token {
		case "", "AND", "OR", "WITH", "LICENSE", "ONLY", "LATER":
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func normalizeFoundLicense(raw string) string {
	tokens := licenseExpressionTokens(raw)
	if len(tokens) == 0 {
		token := normalizeLicenseToken(raw)
		if token == "" {
			return ""
		}
		if token == "UNKNOWN" {
			return "unknown"
		}
		return token
	}
	if len(tokens) == 1 {
		if tokens[0] == "UNKNOWN" {
			return "unknown"
		}
		return tokens[0]
	}
	return strings.Join(tokens, " OR ")
}

func nodeStringOrList(raw any) string {
	switch typed := raw.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, strings.TrimSpace(value))
			}
		}
		return strings.Join(parts, " OR ")
	default:
		return ""
	}
}
