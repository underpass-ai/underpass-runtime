package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestSecurityScanContainerHandler_HeuristicFallbackWhenTrivyMissing(t *testing.T) {
	root := t.TempDir()
	dockerfile := "FROM alpine:latest\nRUN curl -sSL https://example.com/install.sh | sh\n"
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				if spec.Command != "trivy" {
					t.Fatalf("expected first command trivy, got %q", spec.Command)
				}
				return app.CommandResult{
					ExitCode: 127,
					Output:   "sh: 1: trivy: not found",
				}, errors.New("exit 127")
			case 1:
				if spec.Command != "find" {
					t.Fatalf("expected second command find, got %q", spec.Command)
				}
				return app.CommandResult{ExitCode: 0, Output: "./Dockerfile\n"}, nil
			case 2:
				if spec.Command != "cat" {
					t.Fatalf("expected third command cat, got %q", spec.Command)
				}
				return app.CommandResult{ExitCode: 0, Output: dockerfile}, nil
			default:
				t.Fatalf("unexpected command call index %d", callIndex)
				return app.CommandResult{}, nil
			}
		},
	}
	handler := NewSecurityScanContainerHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"path":               ".",
		"max_findings":       10,
		"severity_threshold": "medium",
	}))
	if err != nil {
		t.Fatalf("unexpected security.scan_container error: %#v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected three runner calls, got %d", len(runner.calls))
	}

	output := result.Output.(map[string]any)
	if output["scanner"] != "heuristic-dockerfile" {
		t.Fatalf("expected heuristic scanner, got %#v", output["scanner"])
	}
	if output["findings_count"] == 0 {
		t.Fatalf("expected findings_count > 0, got %#v", output["findings_count"])
	}
}

func TestSecurityScanContainerHandler_HeuristicFallbackWhenTrivyHasNoFindings(t *testing.T) {
	root := t.TempDir()
	dockerfile := "FROM alpine:latest\nRUN chmod 777 /tmp\n"
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile failed: %v", err)
	}

	trivyNoFindings := `{"Results":[{"Target":"go.mod","Class":"lang-pkgs","Type":"gomod"}]}`
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				if spec.Command != "trivy" {
					t.Fatalf("expected first command trivy, got %q", spec.Command)
				}
				return app.CommandResult{
					ExitCode: 0,
					Output:   trivyNoFindings,
				}, nil
			case 1:
				if spec.Command != "find" {
					t.Fatalf("expected second command find, got %q", spec.Command)
				}
				return app.CommandResult{ExitCode: 0, Output: "./Dockerfile\n"}, nil
			case 2:
				if spec.Command != "cat" {
					t.Fatalf("expected third command cat, got %q", spec.Command)
				}
				return app.CommandResult{ExitCode: 0, Output: dockerfile}, nil
			default:
				t.Fatalf("unexpected command call index %d", callIndex)
				return app.CommandResult{}, nil
			}
		},
	}
	handler := NewSecurityScanContainerHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"path":               ".",
		"max_findings":       10,
		"severity_threshold": "medium",
	}))
	if err != nil {
		t.Fatalf("unexpected security.scan_container error: %#v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected three runner calls, got %d", len(runner.calls))
	}

	output := result.Output.(map[string]any)
	if output["scanner"] != "heuristic-dockerfile" {
		t.Fatalf("expected heuristic scanner, got %#v", output["scanner"])
	}
	if output["findings_count"] == 0 {
		t.Fatalf("expected findings_count > 0, got %#v", output["findings_count"])
	}
}

func TestSecurityScanContainerHandler_InvalidSeverity(t *testing.T) {
	handler := NewSecurityScanContainerHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, mustSWERuntimeJSON(t, map[string]any{"severity_threshold": "severe"}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected severity validation error, got %#v", err)
	}
}

func TestSecurityScanContainerHandler_TrivyPath(t *testing.T) {
	raw := `{"Results":[{"Target":"demo","Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH","PkgName":"openssl","InstalledVersion":"1.0","FixedVersion":"1.1","Title":"issue"}]}]}`
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "trivy" {
				t.Fatalf("expected trivy command, got %q", spec.Command)
			}
			return app.CommandResult{ExitCode: 0, Output: raw}, nil
		},
	}
	result, err := NewSecurityScanContainerHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, mustSWERuntimeJSON(t, map[string]any{
		"path":               ".",
		"severity_threshold": "medium",
	}))
	if err != nil {
		t.Fatalf("unexpected trivy path error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["scanner"] != "trivy" {
		t.Fatalf("expected trivy scanner, got %#v", output["scanner"])
	}
}

func TestParseTrivyFindings_AppliesSeverityThreshold(t *testing.T) {
	raw := `{
  "Results": [
    {
      "Target": "alpine:3.20",
      "Vulnerabilities": [
        {"VulnerabilityID":"CVE-1","PkgName":"openssl","InstalledVersion":"1.0","FixedVersion":"1.1","Severity":"HIGH","Title":"high issue"},
        {"VulnerabilityID":"CVE-2","PkgName":"busybox","InstalledVersion":"1.0","FixedVersion":"1.1","Severity":"LOW","Title":"low issue"}
      ]
    }
  ]
}`

	findings, truncated, err := parseTrivyFindings(raw, "high", 50)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if truncated {
		t.Fatal("did not expect truncation")
	}
	if len(findings) != 1 {
		t.Fatalf("expected one finding above threshold, got %d", len(findings))
	}
	if findings[0]["id"] != "CVE-1" {
		t.Fatalf("unexpected finding selected: %#v", findings[0])
	}
}

func TestParseTrivyFindings_WithMisconfigAndSecrets(t *testing.T) {
	report := `{
  "Results": [
    {
      "Target": "image:latest",
      "Vulnerabilities": [
        {"VulnerabilityID":"CVE-1","PkgName":"openssl","InstalledVersion":"1.0","FixedVersion":"1.1","Severity":"HIGH","Title":"high vuln"}
      ],
      "Misconfigurations": [
        {"ID":"MISCONF-1","Type":"Dockerfile","Title":"bad config","Severity":"MEDIUM","Message":"fix me"}
      ],
      "Secrets": [
        {"RuleID":"SECRET-1","Category":"AWS","Title":"aws key","Severity":"CRITICAL","StartLine":12,"Match":"AKIA..."}
      ]
    }
  ]
}`

	findings, truncated, err := parseTrivyFindings(report, "medium", 10)
	if err != nil {
		t.Fatalf("parseTrivyFindings failed: %v", err)
	}
	if truncated {
		t.Fatal("did not expect truncation")
	}
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}
	if findings[0]["severity"] != "critical" {
		t.Fatalf("expected critical finding first, got %#v", findings[0])
	}

	rawArray := `[{"Target":"repo","Vulnerabilities":[{"VulnerabilityID":"CVE-2","Severity":"LOW"}]}]`
	if _, _, err := parseTrivyFindings(rawArray, "low", 1); err != nil {
		t.Fatalf("parseTrivyFindings raw array failed: %v", err)
	}
}

func TestSecurityScanContainerHandler_InvalidArgs(t *testing.T) {
	handler := NewSecurityScanContainerHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestSecurityScanContainerHandler_InvalidPath(t *testing.T) {
	handler := NewSecurityScanContainerHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"path":"../outside"}`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected path validation error, got %#v", err)
	}
}

func TestSecurityScanContainerHandler_TrivyWithImageRef(t *testing.T) {
	raw := `{"Results":[{"Target":"myimage:latest","Vulnerabilities":[{"VulnerabilityID":"CVE-99","Severity":"HIGH","PkgName":"pkg","InstalledVersion":"1.0","FixedVersion":"2.0","Title":"issue"}]}]}`
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "trivy" {
				t.Fatalf("expected trivy, got %q", spec.Command)
			}
			if spec.Args[0] != "image" {
				t.Fatalf("expected image subcommand, got %q", spec.Args[0])
			}
			return app.CommandResult{ExitCode: 0, Output: raw}, nil
		},
	}
	result, err := NewSecurityScanContainerHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, mustSWERuntimeJSON(t, map[string]any{
		"image_ref":          "myimage:latest",
		"severity_threshold": "medium",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["scanner"] != "trivy" {
		t.Fatalf("expected trivy scanner, got %#v", output["scanner"])
	}
	if output["target"] != "myimage:latest" {
		t.Fatalf("expected target=myimage:latest, got %#v", output["target"])
	}
}

func TestSecurityScanContainerHandler_TrivyParseFailFallsBackToHeuristic(t *testing.T) {
	root := t.TempDir()
	dockerfile := "FROM alpine:latest\nRUN curl http://x | sh\n"
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // trivy returns invalid JSON
				return app.CommandResult{ExitCode: 0, Output: "not-json"}, nil
			case 1: // find
				return app.CommandResult{ExitCode: 0, Output: "./Dockerfile\n"}, nil
			case 2: // cat
				return app.CommandResult{ExitCode: 0, Output: dockerfile}, nil
			default:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			}
		},
	}
	result, err := NewSecurityScanContainerHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}, mustSWERuntimeJSON(t, map[string]any{
		"path":               ".",
		"severity_threshold": "medium",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["scanner"] != "heuristic-dockerfile" {
		t.Fatalf("expected heuristic fallback, got %#v", output["scanner"])
	}
}

func TestParseTrivyFindings_EmptyOutput(t *testing.T) {
	_, _, err := parseTrivyFindings("", "medium", 10)
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestParseTrivyFindings_InvalidJSON(t *testing.T) {
	_, _, err := parseTrivyFindings("not-json", "medium", 10)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseTrivyFindings_Truncation(t *testing.T) {
	raw := `{"Results":[{"Target":"demo","Vulnerabilities":[
		{"VulnerabilityID":"CVE-1","Severity":"HIGH","PkgName":"a","InstalledVersion":"1","FixedVersion":"2","Title":"i1"},
		{"VulnerabilityID":"CVE-2","Severity":"HIGH","PkgName":"b","InstalledVersion":"1","FixedVersion":"2","Title":"i2"},
		{"VulnerabilityID":"CVE-3","Severity":"CRITICAL","PkgName":"c","InstalledVersion":"1","FixedVersion":"2","Title":"i3"}
	]}]}`
	findings, truncated, err := parseTrivyFindings(raw, "high", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	// critical should be first (sorted by severity)
	if findings[0]["severity"] != "critical" {
		t.Fatalf("expected critical first, got %#v", findings[0]["severity"])
	}
}

func TestDockerfileHeuristicRule_AllBranches(t *testing.T) {
	// pinned by digest — no finding
	id, _, _ := dockerfileHeuristicRule("from alpine@sha256:abc123")
	if id != "" {
		t.Fatalf("expected no finding for pinned digest, got %q", id)
	}

	// unpinned without tag
	id, sev, _ := dockerfileHeuristicRule("from alpine")
	if id != "dockerfile.unpinned_base_image" || sev != "medium" {
		t.Fatalf("expected unpinned finding, got id=%q sev=%q", id, sev)
	}

	// ADD instruction
	id, sev, _ = dockerfileHeuristicRule("add . /app")
	if id != "dockerfile.add_instead_of_copy" || sev != "low" {
		t.Fatalf("expected ADD finding, got id=%q sev=%q", id, sev)
	}

	// chmod 777
	id, sev, _ = dockerfileHeuristicRule("run chmod 777 /tmp")
	if id != "dockerfile.chmod_777" || sev != "medium" {
		t.Fatalf("expected chmod 777 finding, got id=%q sev=%q", id, sev)
	}

	// apt-get without --no-install-recommends
	id, sev, _ = dockerfileHeuristicRule("run apt-get install -y vim")
	if id != "dockerfile.apt_install_recommends" || sev != "low" {
		t.Fatalf("expected apt finding, got id=%q sev=%q", id, sev)
	}

	// no match
	id, _, _ = dockerfileHeuristicRule("run echo hello")
	if id != "" {
		t.Fatalf("expected no finding for echo, got %q", id)
	}
}

func TestMergeOutputStrings(t *testing.T) {
	if got := mergeOutputStrings("", "new"); got != "new" {
		t.Fatalf("expected 'new', got %q", got)
	}
	if got := mergeOutputStrings("   ", "new"); got != "new" {
		t.Fatalf("expected 'new' for whitespace existing, got %q", got)
	}
	if got := mergeOutputStrings("old", "new"); got != "old\n\nnew" {
		t.Fatalf("expected 'old\\n\\nnew', got %q", got)
	}
}

func TestIsDockerfileCandidate_AllBranches(t *testing.T) {
	if isDockerfileCandidate("") {
		t.Fatal("empty should not be candidate")
	}
	if !isDockerfileCandidate("Dockerfile") {
		t.Fatal("Dockerfile should be candidate")
	}
	if !isDockerfileCandidate("Dockerfile.prod") {
		t.Fatal("Dockerfile.prod should be candidate")
	}
	if !isDockerfileCandidate("app.dockerfile") {
		t.Fatal("app.dockerfile should be candidate")
	}
	if isDockerfileCandidate("README.md") {
		t.Fatal("README.md should not be candidate")
	}
}

// --- applyHeuristicFallback coverage ---

func TestApplyHeuristicFallback_ScanHeuristicsError(t *testing.T) {
	// When scanContainerHeuristics returns an error (find command fails),
	// applyHeuristicFallback should return a domain error.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 1, Output: "find: permission denied"}, errors.New("find failed")
		},
	}
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	_, domErr := applyHeuristicFallback(
		context.Background(), runner, session, ".", "medium", 10,
		heuristicFallbackInput{existingOutput: "some existing output", existingCommand: []string{"trivy", "fs", "."}},
	)
	if domErr == nil {
		t.Fatal("expected domain error when scanContainerHeuristics fails")
	}
	if domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected ErrorCodeExecutionFailed, got %s", domErr.Code)
	}
}

func TestApplyHeuristicFallback_EmptyExistingCommand(t *testing.T) {
	// When existingCommand is nil/empty, applyHeuristicFallback should use
	// the default heuristic command ["heuristic", "dockerfile-scan", scanPath].
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine:latest\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // find
				return app.CommandResult{ExitCode: 0, Output: "./Dockerfile\n"}, nil
			case 1: // cat
				return app.CommandResult{ExitCode: 0, Output: "FROM alpine:latest\n"}, nil
			default:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			}
		},
	}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	result, domErr := applyHeuristicFallback(
		context.Background(), runner, session, ".", "medium", 10,
		heuristicFallbackInput{}, // empty existingOutput and nil existingCommand
	)
	if domErr != nil {
		t.Fatalf("unexpected error: %#v", domErr)
	}
	if len(result.command) != 3 || result.command[0] != "heuristic" {
		t.Fatalf("expected default heuristic command, got %v", result.command)
	}
	if result.scanner != "heuristic-dockerfile" {
		t.Fatalf("expected heuristic-dockerfile scanner, got %q", result.scanner)
	}
}

// --- scanContainerHeuristics coverage ---

func TestScanContainerHeuristics_FindCommandError(t *testing.T) {
	// When the find command itself errors out, scanContainerHeuristics
	// should return an error.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 1, Output: "find: error"}, errors.New("find failed")
		},
	}
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	_, _, _, err := scanContainerHeuristics(context.Background(), runner, session, ".", "medium", 10)
	if err == nil {
		t.Fatal("expected error from scanContainerHeuristics when find fails")
	}
}

func TestScanContainerHeuristics_NoDockerfilesFound(t *testing.T) {
	// When find returns no Dockerfile candidates, we should get zero findings
	// and the output should note no Dockerfile found.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			// find returns only non-Dockerfile files
			return app.CommandResult{ExitCode: 0, Output: "./main.go\n./README.md\n"}, nil
		},
	}
	session := domain.Session{WorkspacePath: t.TempDir(), AllowedPaths: []string{"."}}
	findings, truncated, output, err := scanContainerHeuristics(context.Background(), runner, session, ".", "medium", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected zero findings, got %d", len(findings))
	}
	if truncated {
		t.Fatal("did not expect truncation")
	}
	if !strings.Contains(output, "no Dockerfile found") {
		t.Fatalf("expected 'no Dockerfile found' note in output, got %q", output)
	}
}

func TestScanContainerHeuristics_CatCommandError(t *testing.T) {
	// When cat fails for a Dockerfile, scanContainerHeuristics should
	// continue to the next Dockerfile (not abort).
	root := t.TempDir()
	// We have two Dockerfiles but the cat of the first fails; the second succeeds.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // find
				return app.CommandResult{ExitCode: 0, Output: "./Dockerfile\n./Dockerfile.prod\n"}, nil
			case 1: // cat for first Dockerfile - fails
				return app.CommandResult{ExitCode: 1, Output: "cat: permission denied"}, errors.New("cat failed")
			case 2: // cat for second Dockerfile - succeeds
				return app.CommandResult{ExitCode: 0, Output: "FROM alpine:latest\nRUN chmod 777 /tmp\n"}, nil
			default:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			}
		},
	}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	findings, _, _, err := scanContainerHeuristics(context.Background(), runner, session, ".", "medium", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The second Dockerfile should still produce findings (unpinned + chmod 777 + missing_user).
	if len(findings) == 0 {
		t.Fatal("expected findings from the second Dockerfile despite first cat failing")
	}
}

func TestScanContainerHeuristics_Truncation(t *testing.T) {
	// When findings exceed maxFindings, scanning should stop (truncated=true).
	root := t.TempDir()
	// Dockerfile with many findings
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, spec app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // find
				return app.CommandResult{ExitCode: 0, Output: "./Dockerfile\n"}, nil
			case 1: // cat
				// Several lines that will produce findings — each RUN chmod 777 produces one.
				content := "FROM alpine:latest\nRUN chmod 777 /a\nRUN chmod 777 /b\nRUN chmod 777 /c\n"
				return app.CommandResult{ExitCode: 0, Output: content}, nil
			default:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			}
		},
	}
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}
	// maxFindings=2, so we should get truncated after 2 findings
	findings, truncated, _, err := scanContainerHeuristics(context.Background(), runner, session, ".", "medium", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncation when findings exceed maxFindings")
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
}

// --- scanDockerfileContent coverage ---

func TestScanDockerfileContent_MissingUser(t *testing.T) {
	// Content with no USER instruction should produce a missing_user finding.
	content := "FROM alpine:3.18\nRUN echo hello\n"
	findings, truncated := scanDockerfileContent(nil, content, "Dockerfile", "medium", 10)
	if truncated {
		t.Fatal("did not expect truncation")
	}
	var foundMissingUser bool
	for _, f := range findings {
		if f["id"] == "dockerfile.missing_user" {
			foundMissingUser = true
		}
	}
	if !foundMissingUser {
		t.Fatal("expected missing_user finding when no USER instruction is present")
	}
}

func TestScanDockerfileContent_CommentAndBlankLinesSkipped(t *testing.T) {
	// Comment lines and blank lines should not produce findings.
	content := "# This is a comment\n\nFROM alpine:3.18\nUSER app\n"
	findings, truncated := scanDockerfileContent(nil, content, "Dockerfile", "medium", 10)
	if truncated {
		t.Fatal("did not expect truncation")
	}
	// With a pinned tag and USER present, no rule should fire
	for _, f := range findings {
		if f["id"] == "dockerfile.missing_user" {
			t.Fatal("USER is present, should not get missing_user finding")
		}
	}
}

func TestScanDockerfileContent_TruncationFromMissingUser(t *testing.T) {
	// When maxFindings is already nearly reached and the missing_user finding
	// pushes it over, truncated should become true.
	// Start with one existing finding and set maxFindings=2.
	existing := []map[string]any{
		{"id": "existing", "kind": "misconfiguration", "severity": "high"},
	}
	// Content: one rule match (chmod 777) + missing USER → 2 new findings.
	// existing(1) + chmod 777(1) = 2 = maxFindings → truncated on the first rule match.
	content := "FROM alpine:3.18\nRUN chmod 777 /tmp\n"
	findings, truncated := scanDockerfileContent(existing, content, "Dockerfile", "medium", 2)
	if !truncated {
		t.Fatal("expected truncation when findings reach maxFindings")
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
}

func TestScanDockerfileContent_MissingUserTruncation(t *testing.T) {
	// The missing_user finding itself should cause truncation when it pushes
	// findings exactly to maxFindings.
	// No per-line rule matches, but FROM alpine:3.18 (pinned) + no USER →
	// missing_user is the only finding. Existing makes it hit the cap.
	existing := []map[string]any{
		{"id": "existing1", "kind": "misconfiguration", "severity": "high"},
	}
	content := "FROM alpine:3.18\nRUN echo hello\n"
	findings, truncated := scanDockerfileContent(existing, content, "Dockerfile", "medium", 2)
	if !truncated {
		t.Fatal("expected truncation when missing_user finding reaches maxFindings")
	}
	hasMissingUser := false
	for _, f := range findings {
		if f["id"] == "dockerfile.missing_user" {
			hasMissingUser = true
		}
	}
	if !hasMissingUser {
		t.Fatal("expected missing_user finding")
	}
}
