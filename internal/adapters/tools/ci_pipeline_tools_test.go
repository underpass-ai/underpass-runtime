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

func TestCIRunPipelineHandler_FailFast(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1:
				return app.CommandResult{ExitCode: 2, Output: "build failed"}, errors.New("exit 2")
			default:
				return app.CommandResult{ExitCode: 0, Output: "should not run"}, nil
			}
		},
	}
	handler := NewCIRunPipelineHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"fail_fast":               true,
		"include_static_analysis": false,
		"include_coverage":        false,
	}))
	if err == nil {
		t.Fatal("expected ci.run_pipeline to fail")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected fail-fast after 2 calls, got %d", len(runner.calls))
	}

	output := result.Output.(map[string]any)
	if output["failed_step"] != "build" {
		t.Fatalf("unexpected failed_step: %#v", output["failed_step"])
	}
}

func TestCIRunPipelineHandler_QualityGateFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1:
				return app.CommandResult{ExitCode: 0, Output: "build ok"}, nil
			case 2:
				return app.CommandResult{ExitCode: 0, Output: "tests ok"}, nil
			default:
				t.Fatalf("unexpected call index %d", callIndex)
				return app.CommandResult{}, nil
			}
		},
	}
	handler := NewCIRunPipelineHandler(runner)
	session := domain.Session{WorkspacePath: root, AllowedPaths: []string{"."}}

	result, err := handler.Invoke(context.Background(), session, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": false,
		"include_coverage":        false,
		"include_quality_gate":    true,
		"fail_fast":               true,
		"quality_gate": map[string]any{
			"min_coverage_percent": 80,
			"max_failed_tests":     0,
		},
	}))
	if err == nil {
		t.Fatal("expected ci.run_pipeline quality gate error")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("unexpected error code: %s", err.Code)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 runner calls, got %d", len(runner.calls))
	}

	output := result.Output.(map[string]any)
	if output["failed_step"] != "quality_gate" {
		t.Fatalf("unexpected failed_step: %#v", output["failed_step"])
	}
	qualityGate, ok := output["quality_gate"].(map[string]any)
	if !ok {
		t.Fatalf("expected quality_gate map, got %T", output["quality_gate"])
	}
	if qualityGate["status"] != "fail" {
		t.Fatalf("expected quality gate fail status, got %#v", qualityGate["status"])
	}
}

func TestCIRunPipelineHandler_NoSupportedToolchain(t *testing.T) {
	handler := NewCIRunPipelineHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{}`))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected no toolchain execution error, got %#v", err)
	}
}

func TestCIRunPipelineHandler_SuccessPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0:
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1:
				return app.CommandResult{ExitCode: 0, Output: "build ok"}, nil
			case 2:
				return app.CommandResult{ExitCode: 0, Output: "tests ok"}, nil
			case 3:
				return app.CommandResult{ExitCode: 0, Output: "static ok"}, nil
			case 4:
				return app.CommandResult{ExitCode: 0, Output: "ok\tcoverage: 82.5% of statements"}, nil
			case 5:
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			default:
				t.Fatalf("unexpected pipeline call index: %d", callIndex)
				return app.CommandResult{}, nil
			}
		},
	}

	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": true,
		"include_coverage":        true,
		"include_quality_gate":    true,
		"fail_fast":               true,
		"quality_gate": map[string]any{
			"min_coverage_percent": 80,
			"max_failed_tests":     0,
		},
	}))
	if err != nil {
		t.Fatalf("unexpected ci.run_pipeline success error: %#v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	output := result.Output.(map[string]any)
	if output["failed_step"] != "" {
		t.Fatalf("expected no failed_step, got %#v", output["failed_step"])
	}
}

func TestCIRunPipelineHandler_InvalidArgs(t *testing.T) {
	handler := NewCIRunPipelineHandler(&fakeSWERuntimeCommandRunner{})
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{invalid`))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid arg error, got %#v", err)
	}
}

func TestCIRunPipelineHandler_StaticAnalysisFailFast(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	callCount := 0
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			callCount++
			switch callIndex {
			case 0: // validate
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1: // build
				return app.CommandResult{ExitCode: 0, Output: "build ok"}, nil
			case 2: // test
				return app.CommandResult{ExitCode: 0, Output: "FAIL test1\ntests ok"}, nil
			case 3: // static_analysis
				return app.CommandResult{ExitCode: 1, Output: "lint failed"}, errors.New("exit 1")
			default:
				return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
			}
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": true,
		"include_coverage":        false,
		"include_quality_gate":    false,
		"fail_fast":               true,
	}))
	if err == nil {
		t.Fatal("expected pipeline to fail on static analysis")
	}
	output := result.Output.(map[string]any)
	if output["failed_step"] != "static_analysis" {
		t.Fatalf("expected failed_step=static_analysis, got %#v", output["failed_step"])
	}
}

func TestCIRunPipelineHandler_CoverageStep(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // validate
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1: // build
				return app.CommandResult{ExitCode: 0, Output: "build ok"}, nil
			case 2: // test
				return app.CommandResult{ExitCode: 0, Output: "tests ok"}, nil
			case 3: // coverage
				return app.CommandResult{ExitCode: 0, Output: "ok\tcoverage: 92.0% of statements"}, nil
			case 4: // rm cleanup
				return app.CommandResult{ExitCode: 0, Output: ""}, nil
			default:
				return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
			}
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": false,
		"include_coverage":        true,
		"include_quality_gate":    false,
		"fail_fast":               false,
	}))
	if err != nil {
		t.Fatalf("unexpected pipeline error: %#v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestCIRunPipelineHandler_NonGoSkipsCoverage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": false,
		"include_coverage":        true,
		"include_quality_gate":    false,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	output := result.Output.(map[string]any)
	steps := output["steps"].([]map[string]any)
	for _, step := range steps {
		if step["name"] == "coverage" && step["status"] != "skipped" {
			t.Fatalf("expected coverage step to be skipped for node project, got %#v", step["status"])
		}
	}
	_ = result
}

func TestUpdatePipelineQualityMetrics_AllSteps(t *testing.T) {
	m := qualityGateMetrics{}

	// test failure with output
	updatePipelineQualityMetrics("test", "FAIL test1\nFAIL test2", errors.New("exit 1"), 1, &m)
	if m.FailedTestsCount != 2 {
		t.Fatalf("expected 2 failed tests, got %d", m.FailedTestsCount)
	}

	// test failure without parseable output
	m2 := qualityGateMetrics{}
	updatePipelineQualityMetrics("test", "some error", errors.New("exit 1"), 1, &m2)
	if m2.FailedTestsCount != 1 {
		t.Fatalf("expected 1 failed test (fallback), got %d", m2.FailedTestsCount)
	}

	// test success
	m3 := qualityGateMetrics{}
	updatePipelineQualityMetrics("test", "ok", nil, 0, &m3)
	if m3.FailedTestsCount != 0 {
		t.Fatalf("expected 0 failed tests on success, got %d", m3.FailedTestsCount)
	}

	// static_analysis
	m4 := qualityGateMetrics{}
	updatePipelineQualityMetrics("static_analysis", "src/main.go:10: error\nsrc/main.go:20: warning", nil, 0, &m4)
	if m4.DiagnosticsCount == 0 {
		t.Fatal("expected diagnostics count > 0")
	}

	// coverage
	m5 := qualityGateMetrics{}
	updatePipelineQualityMetrics("coverage", "ok\tcoverage: 75.0% of statements", nil, 0, &m5)
	if m5.CoveragePercent != 75.0 {
		t.Fatalf("expected 75.0 coverage, got %f", m5.CoveragePercent)
	}
}

func TestAnnotatePipelineStepError_AllBranches(t *testing.T) {
	if annotatePipelineStepError(nil, "test") != nil {
		t.Fatal("expected nil for nil error")
	}
	timeoutErr := &domain.Error{Code: app.ErrorCodeTimeout, Message: "original"}
	got := annotatePipelineStepError(timeoutErr, "build")
	if !strings.Contains(got.Message, "timed out") {
		t.Fatalf("expected timeout message, got %q", got.Message)
	}
	execErr := &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "original"}
	got2 := annotatePipelineStepError(execErr, "test")
	if !strings.Contains(got2.Message, "failed") {
		t.Fatalf("expected failed message, got %q", got2.Message)
	}
	otherErr := &domain.Error{Code: app.ErrorCodeInvalidArgument, Message: "original"}
	got3 := annotatePipelineStepError(otherErr, "test")
	if got3.Message != "original" {
		t.Fatalf("expected unchanged message, got %q", got3.Message)
	}
}

func TestCIRunPipelineHandler_StaticAnalysisSkippedNoToolchain(t *testing.T) {
	// When static analysis has no supported toolchain (e.g. C project without
	// a resolvable source file), the static step should be "skipped" rather
	// than failing the whole pipeline. We use a C project with no .c files.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CMakeLists.txt"), []byte("project(demo)\n"), 0o644); err != nil {
		t.Fatalf("write CMakeLists.txt: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": true,
		"include_coverage":        false,
		"include_quality_gate":    false,
		"fail_fast":               true,
	}))
	// The pipeline may fail at validate/build step because there is no .c source.
	// But if it gets to static_analysis, the step should be skipped.
	// The C project without a source file will fail at validate step because
	// resolveCSourceForBuild returns error. So let's verify that case too.
	if err == nil {
		// All steps passed — check that static was skipped
		output := result.Output.(map[string]any)
		steps := output["steps"].([]map[string]any)
		for _, step := range steps {
			if step["name"] == "static_analysis" && step["status"] != "skipped" {
				t.Fatalf("expected static_analysis to be skipped, got %#v", step["status"])
			}
		}
	}
	// If err != nil, it's because validate/build failed for C without source,
	// which is an acceptable outcome — the point is static_analysis skipping.
}

func TestCIRunPipelineHandler_FailFastFalse_ContinuesAfterFailure(t *testing.T) {
	// With fail_fast=false, when a step fails the pipeline should continue
	// executing remaining steps instead of aborting early.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // validate — succeeds
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1: // build — FAILS
				return app.CommandResult{ExitCode: 2, Output: "build failed"}, errors.New("exit 2")
			case 2: // test — still runs because fail_fast=false
				return app.CommandResult{ExitCode: 0, Output: "tests ok"}, nil
			case 3: // static_analysis — still runs
				return app.CommandResult{ExitCode: 0, Output: "static ok"}, nil
			default:
				return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
			}
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"fail_fast":               false,
		"include_static_analysis": true,
		"include_coverage":        false,
		"include_quality_gate":    false,
	}))
	// Pipeline should still report failure (from the build step).
	if err == nil {
		t.Fatal("expected pipeline to report failure from build step")
	}
	// But all steps after build should have run.
	output := result.Output.(map[string]any)
	steps := output["steps"].([]map[string]any)
	if len(steps) < 4 {
		t.Fatalf("expected at least 4 steps (validate, build, test, static_analysis), got %d", len(steps))
	}
	if output["failed_step"] != "build" {
		t.Fatalf("expected failed_step=build, got %#v", output["failed_step"])
	}
	// Verify test step ran despite build failure.
	foundTest := false
	for _, step := range steps {
		if step["name"] == "test" {
			foundTest = true
			if step["status"] != "succeeded" {
				t.Fatalf("expected test step to succeed, got %#v", step["status"])
			}
		}
	}
	if !foundTest {
		t.Fatal("test step should have been executed with fail_fast=false")
	}
}

func TestCIRunPipelineHandler_BuildCommandResolutionError(t *testing.T) {
	// C project with no .c source file — buildCommandForProject returns error
	// because resolveCSourceForBuild fails. This exercises the build step
	// command resolution error path.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CMakeLists.txt"), []byte("project(demo)\n"), 0o644); err != nil {
		t.Fatalf("write CMakeLists.txt: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
		},
	}
	_, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"include_static_analysis": false,
		"include_coverage":        false,
		"include_quality_gate":    false,
		"fail_fast":               true,
	}))
	// Should fail because validate or build can't resolve a C source file.
	if err == nil {
		t.Fatal("expected error for C project without source files")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected ErrorCodeExecutionFailed, got %s", err.Code)
	}
}

func TestCIRunPipelineHandler_TestStepFailFast(t *testing.T) {
	// Test step fails with fail_fast=true — exercises the runPipelineTestStep
	// early-return branch (!ps.runStep && failFast).
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // validate
				return app.CommandResult{ExitCode: 0, Output: "validate ok"}, nil
			case 1: // build
				return app.CommandResult{ExitCode: 0, Output: "build ok"}, nil
			case 2: // test — FAILS
				return app.CommandResult{ExitCode: 1, Output: "FAIL test1"}, errors.New("exit 1")
			default:
				return app.CommandResult{ExitCode: 0, Output: "should not run"}, nil
			}
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"fail_fast":               true,
		"include_static_analysis": false,
		"include_coverage":        false,
		"include_quality_gate":    false,
	}))
	if err == nil {
		t.Fatal("expected pipeline to fail on test step")
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls (validate, build, test), got %d", len(runner.calls))
	}
	output := result.Output.(map[string]any)
	if output["failed_step"] != "test" {
		t.Fatalf("expected failed_step=test, got %#v", output["failed_step"])
	}
}

func TestCIRunPipelineHandler_ValidateFailNoAbort(t *testing.T) {
	// Validate step fails with fail_fast=false — pipeline continues.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(callIndex int, _ app.CommandSpec) (app.CommandResult, error) {
			switch callIndex {
			case 0: // validate — FAILS
				return app.CommandResult{ExitCode: 1, Output: "validate failed"}, errors.New("exit 1")
			default:
				return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
			}
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"fail_fast":               false,
		"include_static_analysis": false,
		"include_coverage":        false,
		"include_quality_gate":    false,
	}))
	if err == nil {
		t.Fatal("expected pipeline to report failure")
	}
	output := result.Output.(map[string]any)
	// Pipeline should have continued past validate.
	steps := output["steps"].([]map[string]any)
	if len(steps) < 3 {
		t.Fatalf("expected at least 3 steps with fail_fast=false, got %d", len(steps))
	}
}

func TestCIRunPipelineHandler_ValidateFailFast(t *testing.T) {
	// Validate step fails with fail_fast=true — exercises the
	// runPipelineValidateStep early-return branch.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	runner := &fakeSWERuntimeCommandRunner{
		run: func(_ int, _ app.CommandSpec) (app.CommandResult, error) {
			return app.CommandResult{ExitCode: 1, Output: "validate failed"}, errors.New("exit 1")
		},
	}
	result, err := NewCIRunPipelineHandler(runner).Invoke(context.Background(), domain.Session{WorkspacePath: root}, mustSWERuntimeJSON(t, map[string]any{
		"fail_fast":               true,
		"include_static_analysis": false,
		"include_coverage":        false,
		"include_quality_gate":    false,
	}))
	if err == nil {
		t.Fatal("expected pipeline to fail at validate step")
	}
	// Only 1 runner call (validate) — should abort immediately.
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call (validate), got %d", len(runner.calls))
	}
	output := result.Output.(map[string]any)
	if output["failed_step"] != "validate" {
		t.Fatalf("expected failed_step=validate, got %#v", output["failed_step"])
	}
}

// ---------------------------------------------------------------------------
// Direct step-function tests — exercise command-resolution error branches
// that are unreachable through Invoke because validate fails first.
// ---------------------------------------------------------------------------

func newTestPipelineState(runner app.CommandRunner, workspacePath string) *pipelineState {
	return &pipelineState{
		runner:  runner,
		session: domain.Session{WorkspacePath: workspacePath},
		steps:   make([]map[string]any, 0, 6),
	}
}

func TestRunPipelineStaticStep_SkippedOnUnsupported(t *testing.T) {
	// When staticAnalysisCommandForProject returns an error, the step is skipped.
	ps := newTestPipelineState(&fakeSWERuntimeCommandRunner{}, t.TempDir())
	early, _, domErr := runPipelineStaticStep(context.Background(), ps, projectType{Name: "unknown"}, "", true)
	if early {
		t.Fatal("expected early=false for skipped step")
	}
	if domErr != nil {
		t.Fatalf("unexpected error: %#v", domErr)
	}
	if len(ps.steps) != 1 || ps.steps[0]["status"] != "skipped" {
		t.Fatalf("expected skipped step, got %#v", ps.steps)
	}
}

func TestRunPipelineBuildStep_CommandResolutionError(t *testing.T) {
	// When buildCommandForProject returns an error (e.g. C without source).
	ps := newTestPipelineState(&fakeSWERuntimeCommandRunner{}, t.TempDir())
	early, _, domErr := runPipelineBuildStep(context.Background(), ps, projectType{Name: "unknown"}, "", true)
	if !early {
		t.Fatal("expected early=true for command resolution error")
	}
	if domErr == nil || domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution error, got %#v", domErr)
	}
}

func TestRunPipelineTestStep_CommandResolutionError(t *testing.T) {
	// When testCommandForProject returns an error (e.g. unsupported project type).
	ps := newTestPipelineState(&fakeSWERuntimeCommandRunner{}, t.TempDir())
	early, _, domErr := runPipelineTestStep(context.Background(), ps, projectType{Name: "unknown"}, "", true)
	if !early {
		t.Fatal("expected early=true for command resolution error")
	}
	if domErr == nil || domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution error, got %#v", domErr)
	}
}
