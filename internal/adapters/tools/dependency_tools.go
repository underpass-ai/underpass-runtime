package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type SecurityScanDependenciesHandler struct {
	runner app.CommandRunner
}

type SBOMGenerateHandler struct {
	runner app.CommandRunner
}

func NewSecurityScanDependenciesHandler(runner app.CommandRunner) *SecurityScanDependenciesHandler {
	return &SecurityScanDependenciesHandler{runner: runner}
}

func NewSBOMGenerateHandler(runner app.CommandRunner) *SBOMGenerateHandler {
	return &SBOMGenerateHandler{runner: runner}
}

func (h *SecurityScanDependenciesHandler) Name() string {
	return "security.scan_dependencies"
}

func (h *SecurityScanDependenciesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path            string `json:"path"`
		MaxDependencies int    `json:"max_dependencies"`
	}{Path: ".", MaxDependencies: 500}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid security.scan_dependencies args",
			Retryable: false,
		}
	}
	request.MaxDependencies = clampInt(request.MaxDependencies, 1, 5000, 500)

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

	runner := ensureRunner(h.runner)
	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported dependency scanning toolchain found",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	inventory, inventoryErr := collectDependencyInventory(ctx, runner, session, detected, scanPath, request.MaxDependencies)
	if inventoryErr != nil {
		if errors.Is(inventoryErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "dependency scanning is not supported for detected project type",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   inventoryErr.Error(),
			Retryable: false,
		}
	}

	dependencyItems := dependencyEntriesToMaps(inventory.Dependencies)
	artifacts := []app.ArtifactPayload{{
		Name:        sweArtifactDepScanOutput,
		ContentType: sweTextPlain,
		Data:        []byte(inventory.Output),
	}}
	if inventoryJSON, marshalErr := json.MarshalIndent(map[string]any{
		"project_type": detected.Name,
		"path":         scanPath,
		"dependencies": dependencyItems,
		"truncated":    inventory.Truncated,
	}, "", "  "); marshalErr == nil {
		artifacts = append(artifacts, app.ArtifactPayload{
			Name:        sweArtifactDepInventory,
			ContentType: sweApplicationJSON,
			Data:        inventoryJSON,
		})
	}

	result := app.ToolRunResult{
		ExitCode: inventory.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: inventory.Output}},
		Output: map[string]any{
			"project_type":       detected.Name,
			"command":            inventory.Command,
			"dependencies_count": len(dependencyItems),
			"dependencies":       dependencyItems,
			"truncated":          inventory.Truncated,
			"scanner":            "workspace-inventory-v1",
			"exit_code":          inventory.ExitCode,
			"output":             inventory.Output,
		},
		Artifacts: artifacts,
	}
	if inventory.RunErr != nil && len(dependencyItems) == 0 {
		return result, toToolError(inventory.RunErr, inventory.Output)
	}
	return result, nil
}

func (h *SBOMGenerateHandler) Name() string {
	return "sbom.generate"
}

func (h *SBOMGenerateHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Path          string `json:"path"`
		Format        string `json:"format"`
		MaxComponents int    `json:"max_components"`
	}{
		Path:          ".",
		Format:        sweCycloneDXJSON,
		MaxComponents: 1000,
	}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid sbom.generate args",
			Retryable: false,
		}
	}

	format := strings.ToLower(strings.TrimSpace(request.Format))
	if format == "" {
		format = sweCycloneDXJSON
	}
	if format != sweCycloneDXJSON {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "format must be cyclonedx-json",
			Retryable: false,
		}
	}
	request.MaxComponents = clampInt(request.MaxComponents, 1, 10000, 1000)

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

	runner := ensureRunner(h.runner)
	detected, detectDomErr := detectProjectTypeOrError(ctx, runner, session, "no supported sbom toolchain found")
	if detectDomErr != nil {
		return app.ToolRunResult{}, detectDomErr
	}

	inventory, inventoryErr := collectDependencyInventory(ctx, runner, session, detected, scanPath, request.MaxComponents)
	if inventoryErr != nil {
		return app.ToolRunResult{}, dependencyInventoryError(inventoryErr, "sbom generation is not supported for detected project type")
	}

	result, buildErr := buildSBOMResult(detected.Name, inventory)
	if buildErr != nil {
		return app.ToolRunResult{}, buildErr
	}
	if inventory.RunErr != nil && len(inventory.Dependencies) == 0 {
		return result, toToolError(inventory.RunErr, inventory.Output)
	}
	return result, nil
}

func buildSBOMResult(projectType string, inventory dependencyInventoryResult) (app.ToolRunResult, *domain.Error) {
	components := buildCycloneDXComponents(inventory.Dependencies)
	sbomDocument := map[string]any{
		"bomFormat":    "CycloneDX",
		"specVersion":  "1.5",
		"version":      1,
		"serialNumber": "",
		"metadata": map[string]any{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"tools": []map[string]any{
				{"vendor": "underpass-ai", "name": "workspace", "version": "v1"},
			},
			"component": map[string]any{
				"type": "application",
				"name": "workspace-repo",
			},
		},
		"components": components,
	}
	sbomBytes, marshalErr := json.MarshalIndent(sbomDocument, "", "  ")
	if marshalErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   "failed to encode sbom document",
			Retryable: false,
		}
	}

	preview := components
	if len(preview) > 25 {
		preview = preview[:25]
	}

	return app.ToolRunResult{
		ExitCode: inventory.ExitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: inventory.Output}},
		Output: map[string]any{
			"project_type":     projectType,
			"format":           sweCycloneDXJSON,
			"generator":        "workspace.sbom.generate/v1",
			"command":          inventory.Command,
			"components_count": len(components),
			"components":       preview,
			"truncated":        inventory.Truncated,
			"artifact_name":    sweArtifactSBOM,
			"exit_code":        inventory.ExitCode,
		},
		Artifacts: []app.ArtifactPayload{
			{
				Name:        sweArtifactSBOM,
				ContentType: sweApplicationJSON,
				Data:        sbomBytes,
			},
			{
				Name:        sweArtifactSBOMOutput,
				ContentType: sweTextPlain,
				Data:        []byte(inventory.Output),
			},
		},
	}, nil
}

type dependencyEntry struct {
	Name      string
	Version   string
	Ecosystem string
	License   string
}

type dependencyInventoryResult struct {
	Command      []string
	ExitCode     int
	Output       string
	Dependencies []dependencyEntry
	Truncated    bool
	RunErr       error
}

func collectDependencyInventory(
	ctx context.Context,
	runner app.CommandRunner,
	session domain.Session,
	detected projectType,
	scanPath string,
	maxDependencies int,
) (dependencyInventoryResult, error) {
	cwd := session.WorkspacePath
	if scanPath != "" && scanPath != "." {
		cwd = filepath.Join(session.WorkspacePath, filepath.FromSlash(scanPath))
	}

	var command string
	var commandArgs []string
	var parser func(string, int) ([]dependencyEntry, bool, error)

	switch detected.Name {
	case sweEcosystemGo:
		command = "go"
		commandArgs = []string{"list", "-m", "all"}
		parser = parseGoDependencyInventory
	case sweEcosystemNode:
		command = "npm"
		commandArgs = []string{"ls", "--json", "--all"}
		parser = parseNodeDependencyInventory
	case sweEcosystemPython:
		pythonExecutable := resolvePythonExecutable(session.WorkspacePath)
		command = pythonExecutable
		commandArgs = []string{"-m", "pip", "list", "--format=json"}
		parser = parsePythonDependencyInventory
	case sweEcosystemRust:
		command = "cargo"
		commandArgs = []string{"tree", "--prefix", "none"}
		parser = parseRustDependencyInventory
	case sweEcosystemJava:
		if detected.Flavor == "gradle" {
			command = "gradle"
			commandArgs = []string{"dependencies", "--configuration", "runtimeClasspath"}
			parser = parseGradleDependencyInventory
		} else {
			command = "mvn"
			commandArgs = []string{"-q", "dependency:list", "-DincludeScope=runtime", "-DoutputAbsoluteArtifactFilename=false"}
			parser = parseMavenDependencyInventory
		}
	default:
		return dependencyInventoryResult{}, os.ErrNotExist
	}

	commandResult, runErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      cwd,
		Command:  command,
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})

	dependencies, truncated, parseErr := parser(commandResult.Output, maxDependencies)
	if parseErr != nil && runErr == nil {
		return dependencyInventoryResult{}, parseErr
	}
	if parseErr != nil && runErr != nil {
		dependencies = nil
		truncated = false
	}

	sort.Slice(dependencies, func(i, j int) bool {
		left := dependencies[i]
		right := dependencies[j]
		if left.Ecosystem != right.Ecosystem {
			return left.Ecosystem < right.Ecosystem
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.Version < right.Version
	})

	return dependencyInventoryResult{
		Command:      append([]string{command}, commandArgs...),
		ExitCode:     commandResult.ExitCode,
		Output:       commandResult.Output,
		Dependencies: dependencies,
		Truncated:    truncated,
		RunErr:       runErr,
	}, nil
}

func parseGoDependencyInventory(output string, maxDependencies int) ([]dependencyEntry, bool, error) {
	lines := splitOutputLines(output)
	out := make([]dependencyEntry, 0, minInt(len(lines), maxDependencies))
	seen := map[string]struct{}{}
	truncated := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}

		version := "unknown"
		for _, token := range fields[1:] {
			token = strings.TrimSpace(token)
			if strings.HasPrefix(token, "v") {
				version = strings.TrimPrefix(token, "v")
				break
			}
		}

		key := sweEcosystemGo + "|" + name + "|" + version
		if _, exists := seen[key]; exists {
			continue
		}
		if len(out) >= maxDependencies {
			truncated = true
			break
		}
		seen[key] = struct{}{}
		out = append(out, dependencyEntry{Name: name, Version: version, Ecosystem: sweEcosystemGo, License: "unknown"})
	}
	return out, truncated, nil
}

func parsePythonDependencyInventory(output string, maxDependencies int) ([]dependencyEntry, bool, error) {
	var payload []map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, false, err
	}

	out := make([]dependencyEntry, 0, minInt(len(payload), maxDependencies))
	seen := map[string]struct{}{}
	truncated := false
	for _, item := range payload {
		name, _ := item["name"].(string)
		version, _ := item["version"].(string)
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)
		if name == "" {
			continue
		}
		if version == "" {
			version = "unknown"
		}
		key := sweEcosystemPython + "|" + strings.ToLower(name) + "|" + version
		if _, exists := seen[key]; exists {
			continue
		}
		if len(out) >= maxDependencies {
			truncated = true
			break
		}
		seen[key] = struct{}{}
		out = append(out, dependencyEntry{Name: name, Version: version, Ecosystem: sweEcosystemPython, License: "unknown"})
	}
	return out, truncated, nil
}

func parseNodeDependencyInventory(output string, maxDependencies int) ([]dependencyEntry, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, false, err
	}

	rootDependencies, _ := payload["dependencies"].(map[string]any)
	out := make([]dependencyEntry, 0, minInt(len(rootDependencies), maxDependencies))
	seen := map[string]struct{}{}
	truncated := false
	walkNodeDependencies(rootDependencies, maxDependencies, seen, &out, &truncated)
	return out, truncated, nil
}

func walkNodeDependencies(
	tree map[string]any,
	maxDependencies int,
	seen map[string]struct{},
	out *[]dependencyEntry,
	truncated *bool,
) {
	if tree == nil || *truncated {
		return
	}

	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if len(*out) >= maxDependencies {
			*truncated = true
			return
		}
		node, ok := extractNodePackageNode(tree, name)
		if !ok {
			continue
		}
		appendNodeDependencyEntry(name, node, seen, out)

		children, _ := node["dependencies"].(map[string]any)
		walkNodeDependencies(children, maxDependencies, seen, out, truncated)
		if *truncated {
			return
		}
	}
}

func extractNodePackageNode(tree map[string]any, name string) (map[string]any, bool) {
	rawNode, ok := tree[name]
	if !ok {
		return nil, false
	}
	node, ok := rawNode.(map[string]any)
	return node, ok
}

func appendNodeDependencyEntry(name string, node map[string]any, seen map[string]struct{}, out *[]dependencyEntry) {
	version, _ := node["version"].(string)
	version = strings.TrimSpace(version)
	if version == "" {
		version = "unknown"
	}
	key := sweEcosystemNode + "|" + name + "|" + version
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	license := normalizeFoundLicense(nodeStringOrList(node["license"]))
	if license == "" {
		license = "unknown"
	}
	*out = append(*out, dependencyEntry{Name: name, Version: version, Ecosystem: sweEcosystemNode, License: license})
}

func parseRustDependencyInventory(output string, maxDependencies int) ([]dependencyEntry, bool, error) {
	lines := splitOutputLines(output)
	out := make([]dependencyEntry, 0, minInt(len(lines), maxDependencies))
	seen := map[string]struct{}{}
	truncated := false
	for _, line := range lines {
		clean := strings.TrimLeft(line, " │├└─`+\\")
		fields := strings.Fields(clean)
		if len(fields) < 2 {
			continue
		}

		name := strings.TrimSpace(fields[0])
		version := strings.TrimSpace(fields[1])
		if strings.HasPrefix(version, "v") {
			version = strings.TrimPrefix(version, "v")
		}
		version = strings.TrimSpace(strings.TrimSuffix(version, ","))
		if name == "" || version == "" {
			continue
		}

		key := sweEcosystemRust + "|" + name + "|" + version
		if _, exists := seen[key]; exists {
			continue
		}
		if len(out) >= maxDependencies {
			truncated = true
			break
		}
		seen[key] = struct{}{}
		out = append(out, dependencyEntry{Name: name, Version: version, Ecosystem: sweEcosystemRust, License: "unknown"})
	}
	return out, truncated, nil
}

func parseMavenDependencyInventory(output string, maxDependencies int) ([]dependencyEntry, bool, error) {
	lines := splitOutputLines(output)
	out := make([]dependencyEntry, 0, minInt(len(lines), maxDependencies))
	seen := map[string]struct{}{}
	truncated := false
	for _, line := range lines {
		clean := strings.TrimSpace(strings.TrimPrefix(line, "[INFO]"))
		if clean == "" || !strings.Contains(clean, ":") {
			continue
		}

		parts := strings.Split(clean, ":")
		if len(parts) < 4 {
			continue
		}
		group := strings.TrimSpace(parts[0])
		artifact := strings.TrimSpace(parts[1])
		version := strings.TrimSpace(parts[3])
		if group == "" || artifact == "" || version == "" {
			continue
		}
		if strings.Contains(group, " ") {
			continue
		}

		name := group + ":" + artifact
		key := sweEcosystemJava + "|" + name + "|" + version
		if _, exists := seen[key]; exists {
			continue
		}
		if len(out) >= maxDependencies {
			truncated = true
			break
		}
		seen[key] = struct{}{}
		out = append(out, dependencyEntry{Name: name, Version: version, Ecosystem: sweEcosystemJava, License: "unknown"})
	}
	return out, truncated, nil
}

func parseGradleDependencyInventory(output string, maxDependencies int) ([]dependencyEntry, bool, error) {
	lines := splitOutputLines(output)
	out := make([]dependencyEntry, 0, minInt(len(lines), maxDependencies))
	seen := map[string]struct{}{}
	truncated := false
	for _, line := range lines {
		name, version, ok := parseGradleCoordinate(line)
		if !ok {
			continue
		}
		key := sweEcosystemJava + "|" + name + "|" + version
		if _, exists := seen[key]; exists {
			continue
		}
		if len(out) >= maxDependencies {
			truncated = true
			break
		}
		seen[key] = struct{}{}
		out = append(out, dependencyEntry{Name: name, Version: version, Ecosystem: sweEcosystemJava, License: "unknown"})
	}
	return out, truncated, nil
}

func parseGradleCoordinate(line string) (name, version string, ok bool) {
	clean := strings.TrimSpace(strings.TrimLeft(line, "+\\|`- "))
	if clean == "" {
		return "", "", false
	}
	parts := strings.Fields(clean)
	if len(parts) == 0 {
		return "", "", false
	}
	coordinate := strings.TrimSpace(parts[0])
	if strings.Count(coordinate, ":") < 2 {
		return "", "", false
	}
	segments := strings.Split(coordinate, ":")
	if len(segments) < 3 {
		return "", "", false
	}
	group := strings.TrimSpace(segments[0])
	artifact := strings.TrimSpace(segments[1])
	version = strings.TrimSpace(segments[2])
	if idx := strings.Index(version, "->"); idx >= 0 {
		version = strings.TrimSpace(version[idx+2:])
	}
	version = strings.TrimSpace(strings.TrimSuffix(version, "(*)"))
	if group == "" || artifact == "" || version == "" {
		return "", "", false
	}
	return group + ":" + artifact, version, true
}

func dependencyEntriesToMaps(entries []dependencyEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, map[string]any{
			"name":      entry.Name,
			"version":   entry.Version,
			"ecosystem": entry.Ecosystem,
			"license":   nonEmptyOrDefault(strings.TrimSpace(entry.License), "unknown"),
		})
	}
	return out
}

func buildCycloneDXComponents(entries []dependencyEntry) []map[string]any {
	components := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		components = append(components, map[string]any{
			"type":    "library",
			"name":    entry.Name,
			"version": entry.Version,
			"purl":    dependencyPURL(entry),
		})
	}
	return components
}

func dependencyPURL(entry dependencyEntry) string {
	name := strings.TrimSpace(entry.Name)
	version := strings.TrimSpace(entry.Version)
	if version == "" {
		version = "unknown"
	}

	switch entry.Ecosystem {
	case sweEcosystemGo:
		return swePURLGolang + name + "@" + version
	case sweEcosystemNode:
		return swePURLNPM + name + "@" + version
	case sweEcosystemPython:
		return swePURLPyPI + strings.ToLower(name) + "@" + version
	case sweEcosystemRust:
		return swePURLCargo + name + "@" + version
	case sweEcosystemJava:
		parts := strings.SplitN(name, ":", 2)
		if len(parts) == 2 {
			return swePURLMaven + parts[0] + "/" + parts[1] + "@" + version
		}
		return swePURLMaven + name + "@" + version
	default:
		return swePURLGeneric + name + "@" + version
	}
}

func cloneDependencyEntries(entries []dependencyEntry) []dependencyEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]dependencyEntry, len(entries))
	copy(out, entries)
	return out
}
