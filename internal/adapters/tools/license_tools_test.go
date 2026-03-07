package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestSecurityLicenseCheckHandler_DeniedLicenseFailsStatus(t *testing.T) {
	root := t.TempDir()
	packageJSON := `{"name":"demo","version":"1.0.0","private":true}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}

	npmOutput := `{
  "name": "demo",
  "version": "1.0.0",
  "dependencies": {
    "left-pad": {
      "version": "1.3.0",
      "license": "GPL-3.0"
    }
  }
}`
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "npm" {
				t.Fatalf("expected npm command, got %q", spec.Command)
			}
			if callIndex == 0 {
				if len(spec.Args) != 3 || spec.Args[0] != "ls" || spec.Args[1] != "--json" || spec.Args[2] != "--all" {
					t.Fatalf("unexpected dependency inventory args: %#v", spec.Args)
				}
			}
			if callIndex == 1 {
				if len(spec.Args) != 4 || spec.Args[3] != "--long" {
					t.Fatalf("unexpected license enrichment args: %#v", spec.Args)
				}
			}
			return app.CommandResult{ExitCode: 0, Output: npmOutput}, nil
		},
	}
	handler := NewSecurityLicenseCheckHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"path":            ".",
		"denied_licenses": []string{"GPL-3.0"},
		"unknown_policy":  "warn",
	}))
	if err != nil {
		t.Fatalf("unexpected security.license_check error: %#v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 npm calls, got %d", len(runner.calls))
	}
	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}

	output := result.Output.(map[string]any)
	if output["status"] != "fail" {
		t.Fatalf("expected fail status, got %#v", output["status"])
	}
	if output["denied_count"] != 1 {
		t.Fatalf("expected denied_count=1, got %#v", output["denied_count"])
	}
}

func TestSecurityLicenseCheckHandler_InvalidUnknownPolicy(t *testing.T) {
	handler := NewSecurityLicenseCheckHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"unknown_policy":"block"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected unknown_policy validation error, got %#v", err)
	}
}

func TestSecurityLicenseCheckHandler_UnknownPolicyDeny(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "go" {
				t.Fatalf("expected go list command, got %q", spec.Command)
			}
			return app.CommandResult{ExitCode: 0, Output: "example.com/demo\nexample.com/lib v1.0.0\n"}, nil
		},
	}
	result, err := NewSecurityLicenseCheckHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"path":           ".",
		"unknown_policy": "deny",
	}))
	if err != nil {
		t.Fatalf("unexpected security.license_check invocation error: %#v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1 for unknown_policy=deny, got %d", result.ExitCode)
	}
}

func TestRuntimeLicenseParsingAndEnrichment(t *testing.T) {
	metadata := `{
  "packages": [
    {"name":"serde","version":"1.0.0","license":"MIT"},
    {"name":"tokio","version":"1.0.0","license":""}
  ]
}`
	rustMap, err := parseRustLicenseMap(metadata, 50)
	if err != nil {
		t.Fatalf("parseRustLicenseMap failed: %v", err)
	}
	if len(rustMap) != 1 {
		t.Fatalf("unexpected rust license map size: %d", len(rustMap))
	}

	nodeRunner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "npm" {
				t.Fatalf("expected npm command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   `{"dependencies":{"left-pad":{"version":"1.3.0","license":"MIT"}}}`,
			}, nil
		},
	}
	nodeEntries := []dependencyEntry{{Name: "left-pad", Version: "1.3.0", Ecosystem: "node", License: "unknown"}}
	enrichedNode, command, _, err := enrichDependencyLicenses(
		context.Background(),
		nodeRunner,
		domain.Session{WorkspacePath: t.TempDir()},
		licenseEnrichmentInput{
			detected: projectType{Name: "node"}, scanPath: ".",
			entries: nodeEntries, maxDependencies: 100,
		},
	)
	if err != nil {
		t.Fatalf("enrichDependencyLicenses node failed: %v", err)
	}
	if len(command) == 0 || command[0] != "npm" {
		t.Fatalf("unexpected node enrichment command: %#v", command)
	}
	if len(enrichedNode) != 1 || enrichedNode[0].License != "MIT" {
		t.Fatalf("unexpected node enriched entries: %#v", enrichedNode)
	}

	rustRunner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "cargo" {
				t.Fatalf("expected cargo command, got %q", spec.Command)
			}
			return app.CommandResult{
				ExitCode: 0,
				Output:   metadata,
			}, nil
		},
	}
	rustEntries := []dependencyEntry{{Name: "serde", Version: "1.0.0", Ecosystem: "rust", License: "unknown"}}
	enrichedRust, rustCommand, _, err := enrichDependencyLicenses(
		context.Background(),
		rustRunner,
		domain.Session{WorkspacePath: t.TempDir()},
		licenseEnrichmentInput{
			detected: projectType{Name: "rust"}, scanPath: ".",
			entries: rustEntries, maxDependencies: 100,
		},
	)
	if err != nil {
		t.Fatalf("enrichDependencyLicenses rust failed: %v", err)
	}
	if len(rustCommand) == 0 || rustCommand[0] != "cargo" {
		t.Fatalf("unexpected rust enrichment command: %#v", rustCommand)
	}
	if len(enrichedRust) != 1 || enrichedRust[0].License != "MIT" {
		t.Fatalf("unexpected rust enriched entries: %#v", enrichedRust)
	}
}

func TestSecurityLicenseCheckHandler_InvalidArgs(t *testing.T) {
	handler := NewSecurityLicenseCheckHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestSecurityLicenseCheckHandler_InvalidPath(t *testing.T) {
	handler := NewSecurityLicenseCheckHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"path":"../outside"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path validation error, got %#v", err)
	}
}

func TestCheckTokensAgainstAllowed_AllBranches(t *testing.T) {
	// token found in allowed set
	status, reason := checkTokensAgainstAllowed([]string{"MIT"}, map[string]struct{}{"MIT": {}}, false)
	if status != "allowed" {
		t.Fatalf("expected allowed, got %q", status)
	}
	if reason != "" {
		t.Fatalf("expected empty reason, got %q", reason)
	}

	// token not found, unknown=true
	status, reason = checkTokensAgainstAllowed([]string{"GPL"}, map[string]struct{}{"MIT": {}}, true)
	if status != "unknown" {
		t.Fatalf("expected unknown, got %q", status)
	}

	// token not found, unknown=false
	status, reason = checkTokensAgainstAllowed([]string{"GPL"}, map[string]struct{}{"MIT": {}}, false)
	if status != "denied" {
		t.Fatalf("expected denied, got %q reason=%q", status, reason)
	}
}

func TestSplitPolicyTokens(t *testing.T) {
	tokens := splitPolicyTokens("MIT, Apache-2.0; GPL-3.0")
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %#v", len(tokens), tokens)
	}
}

func TestParseLicenseCheckRequest_AllBranches(t *testing.T) {
	// valid with all fields
	params, err := parseLicenseCheckRequest(json.RawMessage(`{"path":".","unknown_policy":"deny","denied_licenses":["GPL-3.0"],"allowed_licenses":["MIT"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	if params.unknownPolicy != "deny" {
		t.Fatalf("expected deny, got %q", params.unknownPolicy)
	}

	// empty unknown_policy defaults to warn
	params2, err2 := parseLicenseCheckRequest(json.RawMessage(`{"path":"."}`))
	if err2 != nil {
		t.Fatalf("unexpected error: %#v", err2)
	}
	if params2.unknownPolicy != "warn" {
		t.Fatalf("expected warn default, got %q", params2.unknownPolicy)
	}
}

func TestNormalizeFoundLicense(t *testing.T) {
	if got := normalizeFoundLicense("mit"); got != "MIT" {
		t.Fatalf("expected MIT, got %q", got)
	}
	if got := normalizeFoundLicense(""); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
	if got := normalizeFoundLicense("Apache-2.0 OR MIT"); !strings.Contains(got, "APACHE-2.0") || !strings.Contains(got, "MIT") {
		t.Fatalf("expected normalized with APACHE-2.0 and MIT, got %q", got)
	}
	// Single known token (not a compound expression)
	if got := normalizeFoundLicense("BSD-3-Clause"); got != "BSD-3-CLAUSE" {
		t.Fatalf("expected BSD-3-CLAUSE, got %q", got)
	}
	// Input where licenseExpressionTokens returns nil but normalizeLicenseToken
	// produces a non-empty, non-UNKNOWN value. "OR" is stripped by the tokenizer
	// (it's a keyword), so tokens=[] but normalizeLicenseToken("OR") returns "OR".
	if normalizeFoundLicense("OR") == "" {
		t.Fatal("expected non-empty result for 'OR' input")
	}
}

func TestApplyDependencyLicenses(t *testing.T) {
	entries := []dependencyEntry{
		{Name: "left-pad", Version: "1.0", Ecosystem: "node", License: "unknown"},
		{Name: "right-pad", Version: "2.0", Ecosystem: "node", License: "unknown"},
	}
	licenseMap := map[string]string{
		dependencyLicenseLookupKey("node", "left-pad", "1.0"): "MIT",
	}
	applyDependencyLicenses(entries, licenseMap)
	if entries[0].License != "MIT" {
		t.Fatalf("expected MIT, got %q", entries[0].License)
	}
	if entries[1].License != "unknown" {
		t.Fatalf("expected unknown unchanged, got %q", entries[1].License)
	}

	// empty entries - no panic
	applyDependencyLicenses(nil, licenseMap)
	applyDependencyLicenses(entries, nil)
}

// ---------------------------------------------------------------------------
// Invoke: enrichment fails but entries exist → continues with inventory deps
// ---------------------------------------------------------------------------

func TestSecurityLicenseCheckHandler_EnrichmentFailsButEntriesExist(t *testing.T) {
	root := t.TempDir()
	// Create a node project so detection succeeds.
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			if callIndex == 0 {
				// detectProjectTypeForSession shell probe
				return app.CommandResult{ExitCode: 0, Output: "node:npm"}, nil
			}
			if callIndex == 1 {
				// collectDependencyInventory (npm ls --json --all)
				return app.CommandResult{
					ExitCode: 0,
					Output:   `{"dependencies":{"left-pad":{"version":"1.3.0"}}}`,
				}, nil
			}
			// enrichDependencyLicenses (npm ls --json --all --long) — returns invalid JSON
			// parseNodeLicenseMap will fail but enriched entries from inventory still exist.
			return app.CommandResult{ExitCode: 1, Output: "not json"}, nil
		},
	}

	handler := NewSecurityLicenseCheckHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}, Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes}}

	result, toolErr := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"path":           ".",
		"unknown_policy": "warn",
	}))
	if toolErr != nil {
		t.Fatalf("unexpected error: %#v", toolErr)
	}
	output := result.Output.(map[string]any)
	// Even though enrichment failed, the handler should still produce results
	// using the inventory dependencies.
	if output["project_type"] != "node" {
		t.Fatalf("expected node project, got %v", output["project_type"])
	}
	if output["dependencies_checked"].(int) < 1 {
		t.Fatalf("expected at least 1 dependency, got %v", output["dependencies_checked"])
	}
}

// ---------------------------------------------------------------------------
// Invoke: inventory.RunErr != nil with enrichedEntries empty → returns toToolError
// ---------------------------------------------------------------------------

func TestSecurityLicenseCheckHandler_InventoryRunErrWithEmptyEnriched(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			if callIndex == 0 {
				return app.CommandResult{ExitCode: 0, Output: "node:npm"}, nil
			}
			// Both inventory and enrichment calls return empty results with no
			// parseable deps but with a RunErr (simulated by returning valid
			// JSON with no entries plus a run error).
			return app.CommandResult{
				ExitCode: 1,
				Output:   `{"dependencies":{}}`,
			}, nil
		},
	}

	handler := NewSecurityLicenseCheckHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}, Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes}}

	result, toolErr := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"path":           ".",
		"unknown_policy": "warn",
	}))
	// With 0 dependencies the result should still be returned (pass status).
	// inventory.RunErr is nil here since runner.Run doesn't return an error,
	// so the handler returns normally with zero deps.
	if toolErr != nil {
		t.Fatalf("unexpected error: %#v", toolErr)
	}
	output := result.Output.(map[string]any)
	if output["dependencies_checked"].(int) != 0 {
		t.Fatalf("expected 0 dependencies, got %v", output["dependencies_checked"])
	}
	if output["status"] != "pass" {
		t.Fatalf("expected pass status, got %v", output["status"])
	}
}

// ---------------------------------------------------------------------------
// Invoke: "warn" status path (unknownCount > 0 but no denied)
// ---------------------------------------------------------------------------

func TestSecurityLicenseCheckHandler_WarnStatus(t *testing.T) {
	root := t.TempDir()
	// Go project — enrichDependencyLicenses does nothing for Go (unsupported
	// for enrichment → falls through to default case), so all deps stay
	// "unknown" license.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == "go" {
				return app.CommandResult{
					ExitCode: 0,
					Output:   "example.com/demo\nexample.com/lib v1.0.0\n",
				}, nil
			}
			// detection probe
			return app.CommandResult{ExitCode: 0, Output: "go"}, nil
		},
	}

	handler := NewSecurityLicenseCheckHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, toolErr := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"path":           ".",
		"unknown_policy": "warn",
	}))
	if toolErr != nil {
		t.Fatalf("unexpected error: %#v", toolErr)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "warn" {
		t.Fatalf("expected warn status, got %v", output["status"])
	}
	if output["unknown_count"].(int) == 0 {
		t.Fatalf("expected unknown_count > 0, got %v", output["unknown_count"])
	}
	if output["denied_count"].(int) != 0 {
		t.Fatalf("expected denied_count=0, got %v", output["denied_count"])
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0 for warn, got %d", result.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// enrichDependencyLicenses: empty entries → returns early with nil
// ---------------------------------------------------------------------------

func TestEnrichDependencyLicenses_EmptyEntries(t *testing.T) {
	runner := &fakeSWERuntimeCommandRunner{}
	enriched, command, output, err := enrichDependencyLicenses(
		context.Background(),
		runner,
		domain.Session{WorkspacePath: t.TempDir()},
		licenseEnrichmentInput{
			detected:        projectType{Name: "node"},
			scanPath:        ".",
			entries:         nil,
			maxDependencies: 100,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(enriched) != 0 {
		t.Fatalf("expected empty enriched, got %d entries", len(enriched))
	}
	if command != nil {
		t.Fatalf("expected nil command, got %v", command)
	}
	if output != "" {
		t.Fatalf("expected empty output, got %q", output)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no runner calls, got %d", len(runner.calls))
	}
}

// ---------------------------------------------------------------------------
// enrichDependencyLicenses: unsupported project type → returns enriched, nil
// ---------------------------------------------------------------------------

func TestEnrichDependencyLicenses_DefaultSwitchCase(t *testing.T) {
	entries := []dependencyEntry{
		{Name: "libfoo", Version: "1.0", Ecosystem: "c", License: ""},
	}
	runner := &fakeSWERuntimeCommandRunner{}
	enriched, command, output, err := enrichDependencyLicenses(
		context.Background(),
		runner,
		domain.Session{WorkspacePath: t.TempDir()},
		licenseEnrichmentInput{
			detected:        projectType{Name: "c"},
			scanPath:        ".",
			entries:         entries,
			maxDependencies: 100,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(enriched) != 1 {
		t.Fatalf("expected 1 enriched entry, got %d", len(enriched))
	}
	// Empty license should be normalized to "unknown" in the enriched copy.
	if enriched[0].License != "unknown" {
		t.Fatalf("expected unknown license, got %q", enriched[0].License)
	}
	if command != nil {
		t.Fatalf("expected nil command for unsupported type, got %v", command)
	}
	if output != "" {
		t.Fatalf("expected empty output, got %q", output)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no runner calls for unsupported type, got %d", len(runner.calls))
	}
}

// ---------------------------------------------------------------------------
// enrichDependencyLicenses: Node parse error with no run error → returns parse error
// ---------------------------------------------------------------------------

func TestEnrichDependencyLicenses_NodeParseErrorNoRunErr(t *testing.T) {
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			// Return non-JSON output with no run error — parse will fail.
			return app.CommandResult{ExitCode: 0, Output: "this is not json"}, nil
		},
	}
	entries := []dependencyEntry{
		{Name: "left-pad", Version: "1.0.0", Ecosystem: "node", License: "unknown"},
	}
	enriched, command, _, err := enrichDependencyLicenses(
		context.Background(),
		runner,
		domain.Session{WorkspacePath: t.TempDir()},
		licenseEnrichmentInput{
			detected:        projectType{Name: "node"},
			scanPath:        ".",
			entries:         entries,
			maxDependencies: 100,
		},
	)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if len(enriched) != 1 {
		t.Fatalf("expected enriched entries returned despite error, got %d", len(enriched))
	}
	if len(command) == 0 || command[0] != "npm" {
		t.Fatalf("expected npm command, got %v", command)
	}
}

// ---------------------------------------------------------------------------
// enrichDependencyLicenses: Rust parse error with no run error → returns parse error
// ---------------------------------------------------------------------------

func TestEnrichDependencyLicenses_RustParseErrorNoRunErr(t *testing.T) {
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "not json at all"}, nil
		},
	}
	entries := []dependencyEntry{
		{Name: "serde", Version: "1.0.0", Ecosystem: "rust", License: "unknown"},
	}
	_, command, _, err := enrichDependencyLicenses(
		context.Background(),
		runner,
		domain.Session{WorkspacePath: t.TempDir()},
		licenseEnrichmentInput{
			detected:        projectType{Name: "rust"},
			scanPath:        ".",
			entries:         entries,
			maxDependencies: 100,
		},
	)
	if err == nil {
		t.Fatal("expected parse error for rust, got nil")
	}
	if len(command) == 0 || command[0] != "cargo" {
		t.Fatalf("expected cargo command, got %v", command)
	}
}

// ---------------------------------------------------------------------------
// parseRustLicenseMap: package with empty name → skipped
// ---------------------------------------------------------------------------

func TestParseRustLicenseMap_EmptyName(t *testing.T) {
	metadata := `{"packages":[{"name":"","version":"1.0.0","license":"MIT"}]}`
	m, err := parseRustLicenseMap(metadata, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected empty map for empty-name package, got %d entries", len(m))
	}
}

// ---------------------------------------------------------------------------
// parseRustLicenseMap: truncation at maxDependencies
// ---------------------------------------------------------------------------

func TestParseRustLicenseMap_Truncation(t *testing.T) {
	metadata := `{"packages":[
		{"name":"a","version":"1.0","license":"MIT"},
		{"name":"b","version":"1.0","license":"Apache-2.0"},
		{"name":"c","version":"1.0","license":"BSD-3-Clause"}
	]}`
	m, err := parseRustLicenseMap(metadata, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("expected 2 entries (truncated), got %d", len(m))
	}
}

// ---------------------------------------------------------------------------
// parseRustLicenseMap: package with empty license → skipped
// ---------------------------------------------------------------------------

func TestParseRustLicenseMap_EmptyLicense(t *testing.T) {
	// Empty string normalizes to "unknown", which is skipped.
	metadata := `{"packages":[
		{"name":"valid","version":"1.0","license":"MIT"},
		{"name":"nolicense","version":"1.0","license":""}
	]}`
	m, err := parseRustLicenseMap(metadata, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 entry (empty license skipped), got %d", len(m))
	}
	// Verify that "valid" was kept.
	found := false
	for _, v := range m {
		if v == "MIT" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected MIT in map, got %v", m)
	}
}

// ---------------------------------------------------------------------------
// normalizeFoundLicense: "UNKNOWN" raw → returns "unknown" (single token path)
// ---------------------------------------------------------------------------

func TestNormalizeFoundLicense_UnknownSingleToken(t *testing.T) {
	// "UNKNOWN" produces one token ["UNKNOWN"], len==1, matches tokens[0]=="UNKNOWN"
	if got := normalizeFoundLicense("UNKNOWN"); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// normalizeFoundLicense: N/A raw → "unknown" via normalizeLicenseToken
// ---------------------------------------------------------------------------

func TestNormalizeFoundLicense_NAInput(t *testing.T) {
	// "N/A" is split by "/" into parts ["N","A"], both normalize to "N" and "A"
	// → multi-token → "N OR A"
	// However "NONE" goes through licenseExpressionTokens → parts=["NONE"]
	// → normalizeLicenseToken("NONE") = "UNKNOWN" → tokens=["UNKNOWN"]
	// → len==1, tokens[0]=="UNKNOWN" → returns "unknown"
	if got := normalizeFoundLicense("NONE"); got != "unknown" {
		t.Fatalf("expected unknown for NONE, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// normalizeFoundLicense: multi-token expression like "MIT OR Apache-2.0"
// ---------------------------------------------------------------------------

func TestNormalizeFoundLicense_MultiToken(t *testing.T) {
	got := normalizeFoundLicense("MIT OR Apache-2.0")
	if !strings.Contains(got, "MIT") || !strings.Contains(got, "APACHE-2.0") {
		t.Fatalf("expected MIT and APACHE-2.0, got %q", got)
	}
	if !strings.Contains(got, " OR ") {
		t.Fatalf("expected OR separator, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// normalizeFoundLicense: single non-UNKNOWN token
// ---------------------------------------------------------------------------

func TestNormalizeFoundLicense_SingleNonUnknownToken(t *testing.T) {
	// "bsd-3-clause" → tokens=["BSD-3-CLAUSE"] → len==1, not "UNKNOWN" → return "BSD-3-CLAUSE"
	if got := normalizeFoundLicense("bsd-3-clause"); got != "BSD-3-CLAUSE" {
		t.Fatalf("expected BSD-3-CLAUSE, got %q", got)
	}
}

func TestLicenseClassification_Verdict(t *testing.T) {
	denied := []dependencyEntry{{Name: "bad", Version: "1.0", Ecosystem: "go", License: "GPL-3.0"}}
	c := classifyLicenseEntries(denied, nil, []string{"GPL-3.0"}, "warn")
	if c.Status != "fail" || c.ExitCode != 1 || c.DeniedCount != 1 {
		t.Fatalf("expected fail/1/denied=1, got %s/%d/denied=%d", c.Status, c.ExitCode, c.DeniedCount)
	}

	unknown := []dependencyEntry{{Name: "mystery", Version: "1.0", Ecosystem: "go", License: ""}}
	c = classifyLicenseEntries(unknown, nil, nil, "deny")
	if c.Status != "fail" || c.ExitCode != 1 {
		t.Fatalf("expected fail/1 for unknown+deny, got %s/%d", c.Status, c.ExitCode)
	}

	c = classifyLicenseEntries(unknown, nil, nil, "warn")
	if c.Status != "warn" || c.ExitCode != 0 {
		t.Fatalf("expected warn/0, got %s/%d", c.Status, c.ExitCode)
	}

	allowed := []dependencyEntry{{Name: "ok", Version: "1.0", Ecosystem: "go", License: "MIT"}}
	c = classifyLicenseEntries(allowed, nil, nil, "warn")
	if c.Status != "pass" || c.ExitCode != 0 || c.AllowedCount != 1 {
		t.Fatalf("expected pass/0/allowed=1, got %s/%d/allowed=%d", c.Status, c.ExitCode, c.AllowedCount)
	}
}
