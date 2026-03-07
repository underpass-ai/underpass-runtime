package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeSWERuntimeCommandRunner struct {
	calls []app.CommandSpec
	run   func(callIndex int, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeSWERuntimeCommandRunner) Run(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if f.run != nil {
		return f.run(len(f.calls)-1, spec)
	}
	return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
}

func mustSWERuntimeJSON(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func TestSWERuntimeHandlerNames(t *testing.T) {
	runner := &fakeSWERuntimeCommandRunner{}
	cases := []struct {
		name string
		got  string
	}{
		{name: "repo.coverage_report", got: NewRepoCoverageReportHandler(runner).Name()},
		{name: "repo.static_analysis", got: NewRepoStaticAnalysisHandler(runner).Name()},
		{name: "repo.package", got: NewRepoPackageHandler(runner).Name()},
		{name: "security.scan_secrets", got: NewSecurityScanSecretsHandler(runner).Name()},
		{name: "security.scan_dependencies", got: NewSecurityScanDependenciesHandler(runner).Name()},
		{name: "sbom.generate", got: NewSBOMGenerateHandler(runner).Name()},
		{name: "security.scan_container", got: NewSecurityScanContainerHandler(runner).Name()},
		{name: "security.license_check", got: NewSecurityLicenseCheckHandler(runner).Name()},
		{name: "quality.gate", got: NewQualityGateHandler(runner).Name()},
		{name: "ci.run_pipeline", got: NewCIRunPipelineHandler(runner).Name()},
	}

	for _, tc := range cases {
		if tc.got != tc.name {
			t.Fatalf("unexpected handler name: got=%q want=%q", tc.got, tc.name)
		}
	}
}

func TestRuntimeScalarConverters(t *testing.T) {
	if got := intFromAny(json.Number("12")); got != 12 {
		t.Fatalf("unexpected intFromAny json.Number: %d", got)
	}
	if got := intFromAny(" 7 "); got != 7 {
		t.Fatalf("unexpected intFromAny string: %d", got)
	}
	if got := intFromAny(uint16(9)); got != 9 {
		t.Fatalf("unexpected intFromAny uint16: %d", got)
	}
	if got := intFromAny("bad"); got != 0 {
		t.Fatalf("unexpected intFromAny fallback: %d", got)
	}

	if got := floatFromAny(json.Number("3.5")); got != 3.5 {
		t.Fatalf("unexpected floatFromAny json.Number: %f", got)
	}
	if got := floatFromAny(" 2.25 "); got != 2.25 {
		t.Fatalf("unexpected floatFromAny string: %f", got)
	}
	if got := floatFromAny(int64(5)); got != 5 {
		t.Fatalf("unexpected floatFromAny int64: %f", got)
	}
	if got := floatFromAny("oops"); got != 0 {
		t.Fatalf("unexpected floatFromAny fallback: %f", got)
	}
}

func TestRuntimeSecurityHelpers(t *testing.T) {
	threshold, err := normalizeSeverityThreshold("moderate")
	if err != nil {
		t.Fatalf("normalizeSeverityThreshold failed: %v", err)
	}
	if threshold != "medium" {
		t.Fatalf("unexpected normalized threshold: %q", threshold)
	}
	if _, err := normalizeSeverityThreshold("severe"); err == nil {
		t.Fatal("expected normalizeSeverityThreshold error")
	}

	levels := severityListForThreshold("high")
	if len(levels) != 2 || levels[0] != "CRITICAL" || levels[1] != "HIGH" {
		t.Fatalf("unexpected severity levels: %#v", levels)
	}
	if !severityAtOrAbove("critical", "high") {
		t.Fatal("expected critical to be above high")
	}
	if severityAtOrAbove("low", "high") {
		t.Fatal("low must not pass high threshold")
	}

	if id, sev, _ := dockerfileHeuristicRule("run curl -s https://x | sh"); id == "" || sev != "high" {
		t.Fatalf("unexpected dockerfile heuristic result: id=%q sev=%q", id, sev)
	}
	if !isDockerfileCandidate("Dockerfile.prod") || isDockerfileCandidate("README.md") {
		t.Fatal("unexpected Dockerfile candidate detection")
	}
}

func TestRuntimeLicensePolicyHelpers(t *testing.T) {
	allowed, reason := evaluateLicenseAgainstPolicy("MIT", []string{"MIT"}, []string{"GPL-3.0"})
	if allowed != "allowed" || reason != "" {
		t.Fatalf("expected allowed license, got status=%q reason=%q", allowed, reason)
	}
	denied, reason := evaluateLicenseAgainstPolicy("GPL-3.0", nil, []string{"GPL-3.0"})
	if denied != "denied" || !strings.Contains(reason, "denied") {
		t.Fatalf("expected denied license, got status=%q reason=%q", denied, reason)
	}
	unknown, _ := evaluateLicenseAgainstPolicy("", nil, nil)
	if unknown != "unknown" {
		t.Fatalf("expected unknown license status, got %q", unknown)
	}

	if got := normalizeFoundLicense("mit or apache-2.0"); got != "MIT OR APACHE-2.0" {
		t.Fatalf("unexpected normalized license: %q", got)
	}
	if got := nodeStringOrList([]any{"MIT", "Apache-2.0"}); got != "MIT OR Apache-2.0" {
		t.Fatalf("unexpected nodeStringOrList result: %q", got)
	}
}

func TestIntFromAny_AllBranches(t *testing.T) {
	cases := []struct {
		input    any
		expected int
	}{
		{int(5), 5},
		{int8(3), 3},
		{int16(4), 4},
		{int32(6), 6},
		{int64(7), 7},
		{uint(8), 8},
		{uint8(9), 9},
		{uint16(10), 10},
		{uint32(11), 11},
		{uint64(12), 12},
		{float32(3.9), 3},
		{float64(4.7), 4},
		{json.Number("99"), 99},
		{json.Number("3.5"), 3},  // fallback to Float64 parse
		{"42", 42},
		{"bad", 0},
		{nil, 0},
		{true, 0},
	}
	for _, tc := range cases {
		got := intFromAny(tc.input)
		if got != tc.expected {
			t.Errorf("intFromAny(%T(%v)) = %d, want %d", tc.input, tc.input, got, tc.expected)
		}
	}
}

func TestFloatFromAny_AllBranches(t *testing.T) {
	cases := []struct {
		input    any
		expected float64
	}{
		{float32(1.5), 1.5},
		{float64(2.5), 2.5},
		{int(3), 3.0},
		{int8(4), 4.0},
		{int16(5), 5.0},
		{int32(6), 6.0},
		{int64(7), 7.0},
		{uint(8), 8.0},
		{uint8(9), 9.0},
		{uint16(10), 10.0},
		{uint32(11), 11.0},
		{uint64(12), 12.0},
		{json.Number("3.14"), 3.14},
		{"1.25", 1.25},
		{"bad", 0},
		{nil, 0},
		{true, 0},
	}
	for _, tc := range cases {
		got := floatFromAny(tc.input)
		// float32 conversion loses precision, so use approximate comparison for that case
		if _, isF32 := tc.input.(float32); isF32 {
			if got < tc.expected-0.01 || got > tc.expected+0.01 {
				t.Errorf("floatFromAny(%T(%v)) = %f, want ~%f", tc.input, tc.input, got, tc.expected)
			}
		} else if got != tc.expected {
			t.Errorf("floatFromAny(%T(%v)) = %f, want %f", tc.input, tc.input, got, tc.expected)
		}
	}
}

func TestTruncateString(t *testing.T) {
	if got := truncateString("hello world", 5); got != "hello" {
		t.Fatalf("unexpected truncateString: %q", got)
	}
	if got := truncateString("hi", 10); got != "hi" {
		t.Fatalf("expected no truncation: %q", got)
	}
	if got := truncateString("anything", 0); got != "" {
		t.Fatalf("expected empty for maxLen=0: %q", got)
	}
}

func TestNormalizeSeverityThreshold_AllBranches(t *testing.T) {
	cases := []struct {
		input string
		want  string
		err   bool
	}{
		{"", "medium", false},
		{"low", "low", false},
		{"high", "high", false},
		{"critical", "critical", false},
		{"medium", "medium", false},
		{"moderate", "medium", false},
		{"CRITICAL", "critical", false},
		{"invalid", "", true},
	}
	for _, tc := range cases {
		got, err := normalizeSeverityThreshold(tc.input)
		if tc.err && err == nil {
			t.Errorf("normalizeSeverityThreshold(%q): expected error", tc.input)
		}
		if !tc.err && got != tc.want {
			t.Errorf("normalizeSeverityThreshold(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSeverityListForThreshold_AllBranches(t *testing.T) {
	if levels := severityListForThreshold("critical"); len(levels) != 1 {
		t.Fatalf("expected 1 level for critical, got %d", len(levels))
	}
	if levels := severityListForThreshold("low"); len(levels) != 4 {
		t.Fatalf("expected 4 levels for low, got %d", len(levels))
	}
	if levels := severityListForThreshold("medium"); len(levels) != 3 {
		t.Fatalf("expected 3 levels for medium, got %d", len(levels))
	}
}

func TestNormalizeFindingSeverity_AllBranches(t *testing.T) {
	if got := normalizeFindingSeverity("CRITICAL"); got != "critical" {
		t.Fatalf("expected critical, got %q", got)
	}
	if got := normalizeFindingSeverity("HIGH"); got != "high" {
		t.Fatalf("expected high, got %q", got)
	}
	if got := normalizeFindingSeverity("MODERATE"); got != "medium" {
		t.Fatalf("expected medium, got %q", got)
	}
	if got := normalizeFindingSeverity("LOW"); got != "low" {
		t.Fatalf("expected low, got %q", got)
	}
	if got := normalizeFindingSeverity("bogus"); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
}

func TestSecuritySeverityRank_AllBranches(t *testing.T) {
	if securitySeverityRank("critical") != 4 { t.Fatal("critical should be 4") }
	if securitySeverityRank("high") != 3 { t.Fatal("high should be 3") }
	if securitySeverityRank("medium") != 2 { t.Fatal("medium should be 2") }
	if securitySeverityRank("low") != 1 { t.Fatal("low should be 1") }
	if securitySeverityRank("bogus") != 0 { t.Fatal("unknown should be 0") }
}

func TestDependencyInventoryError(t *testing.T) {
	notExist := dependencyInventoryError(os.ErrNotExist, "not supported")
	if notExist.Message != "not supported" {
		t.Fatalf("expected 'not supported', got %q", notExist.Message)
	}
	other := dependencyInventoryError(errors.New("boom"), "not supported")
	if other.Message != "boom" {
		t.Fatalf("expected 'boom', got %q", other.Message)
	}
}

func TestDetectProjectTypeOrError_NonErrNotExist(t *testing.T) {
	// When detectProjectTypeForSession returns an error that is NOT
	// os.ErrNotExist, detectProjectTypeOrError should return the raw
	// error message (not the notFoundMsg). We simulate this by using
	// an empty workspace (no project files) with a runner that returns
	// a non-ErrNotExist error and a workspace path that also has no
	// recognizable project files for the local fallback.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			// Return a generic error that is NOT os.ErrNotExist.
			return app.CommandResult{ExitCode: 1, Output: ""}, errors.New("network timeout")
		},
	}
	// Use an empty temp dir with no project files so
	// detectProjectTypeFromWorkspace also fails.
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := detectProjectTypeOrError(context.Background(), runner, session, "no toolchain")
	if domErr == nil {
		t.Fatal("expected domain error")
	}
	if domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected ErrorCodeExecutionFailed, got %s", domErr.Code)
	}
	// The message should be the raw error ("network timeout"), not the
	// notFoundMsg ("no toolchain").
	if domErr.Message != "network timeout" {
		t.Fatalf("expected raw error message, got %q", domErr.Message)
	}
}

func TestDetectProjectTypeOrError_ErrNotExist(t *testing.T) {
	// When the project type cannot be detected (ErrNotExist path),
	// detectProjectTypeOrError should return the notFoundMsg.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			// Return "unknown" marker so parseProjectMarker returns unknown.
			return app.CommandResult{ExitCode: 0, Output: "unknown\n"}, nil
		},
	}
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := detectProjectTypeOrError(context.Background(), runner, session, "no toolchain found")
	if domErr == nil {
		t.Fatal("expected domain error for unknown project type")
	}
	if domErr.Message != "no toolchain found" {
		t.Fatalf("expected notFoundMsg, got %q", domErr.Message)
	}
}

func TestDetectProjectTypeOrError_Success(t *testing.T) {
	// When detectProjectTypeForSession succeeds, no error should be returned.
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "go\n"}, nil
		},
	}
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	detected, domErr := detectProjectTypeOrError(context.Background(), runner, session, "no toolchain")
	if domErr != nil {
		t.Fatalf("unexpected error: %#v", domErr)
	}
	if detected.Name != "go" {
		t.Fatalf("expected go project type, got %q", detected.Name)
	}
}
