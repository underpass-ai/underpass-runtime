package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type CIRunPipelineHandler struct {
	runner app.CommandRunner
}

func NewCIRunPipelineHandler(runner app.CommandRunner) *CIRunPipelineHandler {
	return &CIRunPipelineHandler{runner: runner}
}

func (h *CIRunPipelineHandler) Name() string {
	return "ci.run_pipeline"
}

// pipelineState holds the mutable accumulator shared across pipeline steps.
type pipelineState struct {
	runner         app.CommandRunner
	session        domain.Session
	steps          []map[string]any
	combinedOutput strings.Builder
	failedStep     string
	finalExitCode  int
	finalErr       *domain.Error
	qualityMetrics qualityGateMetrics
}

// runStep executes a single pipeline step, appends its result to the state,
// and returns false when the step failed.
func (ps *pipelineState) runStep(ctx context.Context, stepName, command string, commandArgs []string) bool {
	result, runErr := ps.runner.Run(ctx, ps.session, app.CommandSpec{
		Cwd:      ps.session.WorkspacePath,
		Command:  command,
		Args:     commandArgs,
		MaxBytes: 2 * 1024 * 1024,
	})
	status := sweStepSucceeded
	if runErr != nil {
		status = sweStepFailed
	}
	if strings.TrimSpace(result.Output) != "" {
		if ps.combinedOutput.Len() > 0 {
			ps.combinedOutput.WriteString("\n")
		}
		ps.combinedOutput.WriteString("[" + stepName + "]\n")
		ps.combinedOutput.WriteString(result.Output)
	}
	ps.steps = append(ps.steps, map[string]any{
		"name":      stepName,
		"status":    status,
		"command":   append([]string{command}, commandArgs...),
		"exit_code": result.ExitCode,
	})
	updatePipelineQualityMetrics(stepName, result.Output, runErr, result.ExitCode, &ps.qualityMetrics)
	if runErr != nil {
		ps.failedStep = stepName
		ps.finalExitCode = result.ExitCode
		ps.finalErr = annotatePipelineStepError(toToolError(runErr, result.Output), stepName)
		return false
	}
	return true
}

func (h *CIRunPipelineHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target             string                       `json:"target"`
		IncludeStatic      bool                         `json:"include_static_analysis"`
		IncludeCoverage    bool                         `json:"include_coverage"`
		IncludeQualityGate bool                         `json:"include_quality_gate"`
		FailFast           bool                         `json:"fail_fast"`
		QualityGate        qualityGateThresholdsRequest `json:"quality_gate"`
	}{IncludeStatic: true, IncludeCoverage: true, IncludeQualityGate: true, FailFast: true}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid ci.run_pipeline args",
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	detected, detectDomErr := detectProjectTypeOrError(ctx, runner, session, "no supported toolchain found")
	if detectDomErr != nil {
		return app.ToolRunResult{}, detectDomErr
	}

	target := sanitizeTarget(request.Target)
	ps := &pipelineState{
		runner:   runner,
		session:  session,
		steps:    make([]map[string]any, 0, 6),
		finalErr: nil,
	}
	qualityConfig := normalizeQualityGateConfig(request.QualityGate)

	if early, result, err := runPipelineValidateStep(ctx, ps, detected, target, request.FailFast); early {
		return result, err
	}

	if early, result, err := runPipelineBuildStep(ctx, ps, detected, target, request.FailFast); early {
		return result, err
	}

	if early, result, err := runPipelineTestStep(ctx, ps, detected, target, request.FailFast); early {
		return result, err
	}

	if request.IncludeStatic {
		if early, result, err := runPipelineStaticStep(ctx, ps, detected, target, request.FailFast); early {
			return result, err
		}
	}
	if request.IncludeCoverage {
		if early, result, err := runPipelineCoverageStep(ctx, ps, ps.runner, ps.session, detected, target, request.FailFast); early {
			return result, err
		}
	}

	return ps.finalize(detected.Name, qualityConfig)
}

// finalize produces the final pipeline result. The pipelineState knows its own
// accumulated steps, failures, and quality metrics — SRP.
func (ps *pipelineState) finalize(projectName string, qualityConfig qualityGateConfig) (app.ToolRunResult, *domain.Error) {
	var qualityGateOutput map[string]any
	if ps.failedStep == "" {
		qualityGateOutput = runPipelineQualityGateStep(ps, qualityConfig)
	}

	pipelineResult := ciPipelineResult(projectName, ps.steps, ps.failedStep, ps.finalExitCode, ps.combinedOutput.String())
	attachPipelineQualityGateOutput(&pipelineResult, projectName, ps.qualityMetrics, qualityGateOutput)

	if ps.failedStep != "" {
		if ps.finalErr == nil {
			ps.finalErr = &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "pipeline step failed: " + ps.failedStep, Retryable: false}
		}
		return pipelineResult, ps.finalErr
	}
	return pipelineResult, nil
}

// runPipelineStaticStep runs the optional static-analysis step. It returns
// early=true when fail-fast should abort the pipeline.
func runPipelineStaticStep(ctx context.Context, ps *pipelineState, detected projectType, target string, failFast bool) (bool, app.ToolRunResult, *domain.Error) {
	staticCommand, staticArgs, staticErr := staticAnalysisCommandForProject(ps.session.WorkspacePath, detected, target)
	if staticErr != nil {
		ps.steps = append(ps.steps, map[string]any{"name": sweStepStaticAnalysis, "status": sweStepSkipped, "command": []string{}, "exit_code": 0})
		return false, app.ToolRunResult{}, nil
	}
	if !ps.runStep(ctx, sweStepStaticAnalysis, staticCommand, staticArgs) && failFast {
		return true, ciPipelineResult(detected.Name, ps.steps, ps.failedStep, ps.finalExitCode, ps.combinedOutput.String()), ps.finalErr
	}
	return false, app.ToolRunResult{}, nil
}

// runPipelineValidateStep runs the validate step. It returns early=true
// when the command resolution fails or fail-fast aborts the pipeline.
func runPipelineValidateStep(ctx context.Context, ps *pipelineState, detected projectType, target string, failFast bool) (bool, app.ToolRunResult, *domain.Error) {
	command, args, err := validateCommandForProject(ps.session.WorkspacePath, detected, target)
	if err != nil {
		return true, app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if !ps.runStep(ctx, sweStepValidate, command, args) && failFast {
		return true, ciPipelineResult(detected.Name, ps.steps, ps.failedStep, ps.finalExitCode, ps.combinedOutput.String()), ps.finalErr
	}
	return false, app.ToolRunResult{}, nil
}

// runPipelineBuildStep runs the build step. It returns early=true
// when the command resolution fails or fail-fast aborts the pipeline.
func runPipelineBuildStep(ctx context.Context, ps *pipelineState, detected projectType, target string, failFast bool) (bool, app.ToolRunResult, *domain.Error) {
	command, args, err := buildCommandForProject(ps.session.WorkspacePath, detected, target, nil)
	if err != nil {
		return true, app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if !ps.runStep(ctx, sweStepBuild, command, args) && failFast {
		return true, ciPipelineResult(detected.Name, ps.steps, ps.failedStep, ps.finalExitCode, ps.combinedOutput.String()), ps.finalErr
	}
	return false, app.ToolRunResult{}, nil
}

// runPipelineTestStep runs the test step. It returns early=true
// when the command resolution fails or fail-fast aborts the pipeline.
func runPipelineTestStep(ctx context.Context, ps *pipelineState, detected projectType, target string, failFast bool) (bool, app.ToolRunResult, *domain.Error) {
	command, args, err := testCommandForProject(ps.session.WorkspacePath, detected, target, nil)
	if err != nil {
		return true, app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: err.Error(), Retryable: false}
	}
	if !ps.runStep(ctx, sweStepTest, command, args) && failFast {
		return true, ciPipelineResult(detected.Name, ps.steps, ps.failedStep, ps.finalExitCode, ps.combinedOutput.String()), ps.finalErr
	}
	return false, app.ToolRunResult{}, nil
}

// runPipelineCoverageStep runs the optional coverage step. It returns
// early=true when fail-fast should abort the pipeline.
func runPipelineCoverageStep(ctx context.Context, ps *pipelineState, runner app.CommandRunner, session domain.Session, detected projectType, target string, failFast bool) (bool, app.ToolRunResult, *domain.Error) {
	if detected.Name != sweEcosystemGo {
		ps.steps = append(ps.steps, map[string]any{"name": sweStepCoverage, "status": sweStepSkipped, "command": []string{}, "exit_code": 0})
		return false, app.ToolRunResult{}, nil
	}
	coverageFile := ".workspace.cover.out"
	if !ps.runStep(ctx, sweStepCoverage, "go", []string{"test", targetOrDefault(target, "./..."), sweCoverProfile, coverageFile, sweCoverModeAtomic}) && failFast {
		return true, ciPipelineResult(detected.Name, ps.steps, ps.failedStep, ps.finalExitCode, ps.combinedOutput.String()), ps.finalErr
	}
	_, _ = runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "rm",
		Args:     []string{"-f", coverageFile},
		MaxBytes: 16 * 1024,
	})
	return false, app.ToolRunResult{}, nil
}

// runPipelineQualityGateStep evaluates the quality gate, appends its step to
// ps, and returns the gate output map (or nil when the gate passes).
func runPipelineQualityGateStep(ps *pipelineState, qualityConfig qualityGateConfig) map[string]any {
	rules, passed := evaluateQualityGate(ps.qualityMetrics, qualityConfig)
	failedRules := countFailedQualityRules(rules)
	gateStatus := sweStepSucceeded
	gateExitCode := 0
	if !passed {
		gateStatus = sweStepFailed
		gateExitCode = 1
	}
	qualityGateOutput := map[string]any{
		"status":             ternaryQualityGateStatus(passed),
		"passed":             passed,
		"failed_rules_count": failedRules,
		"rules":              rules,
		"thresholds":         qualityGateConfigToMap(qualityConfig),
		"summary":            qualityGateSummary(passed, len(rules)-failedRules, len(rules)),
	}
	ps.steps = append(ps.steps, map[string]any{
		"name":         sweStepQualityGate,
		"status":       gateStatus,
		"command":      []string{sweStepQualityGate},
		"exit_code":    gateExitCode,
		"failed_rules": failedRules,
	})
	if !passed && ps.failedStep == "" {
		ps.failedStep = sweStepQualityGate
		ps.finalExitCode = 1
		ps.finalErr = &domain.Error{Code: app.ErrorCodeExecutionFailed, Message: "pipeline quality gate failed", Retryable: false}
	}
	return qualityGateOutput
}

// attachPipelineQualityGateOutput merges quality-gate data into pipelineResult.
func attachPipelineQualityGateOutput(pipelineResult *app.ToolRunResult, projectType string, metrics qualityGateMetrics, qualityGateOutput map[string]any) {
	if outputMap, ok := pipelineResult.Output.(map[string]any); ok {
		outputMap["quality_metrics"] = qualityGateMetricsToMap(metrics)
		if qualityGateOutput != nil {
			outputMap["quality_gate"] = qualityGateOutput
		}
	}
	if qualityGateOutput == nil {
		return
	}
	if reportBytes, marshalErr := json.MarshalIndent(map[string]any{
		"project_type":    projectType,
		"quality_metrics": qualityGateMetricsToMap(metrics),
		"quality_gate":    qualityGateOutput,
	}, "", "  "); marshalErr == nil {
		pipelineResult.Artifacts = append(pipelineResult.Artifacts, app.ArtifactPayload{
			Name:        sweArtifactQualityGateReport,
			ContentType: sweApplicationJSON,
			Data:        reportBytes,
		})
	}
}

func updatePipelineQualityMetrics(stepName, output string, runErr error, exitCode int, metrics *qualityGateMetrics) {
	switch stepName {
	case sweStepTest:
		if runErr != nil || exitCode != 0 {
			failedTests := summarizeTestFailures(output, 200)
			if len(failedTests) == 0 {
				metrics.FailedTestsCount = 1
			} else {
				metrics.FailedTestsCount = len(failedTests)
			}
		} else {
			metrics.FailedTestsCount = 0
		}
	case sweStepStaticAnalysis:
		metrics.DiagnosticsCount = len(extractDiagnostics(output, 200))
	case sweStepCoverage:
		if parsed := parseCoveragePercent(output); parsed != nil {
			metrics.CoveragePercent = *parsed
		}
	}
}

func annotatePipelineStepError(err *domain.Error, stepName string) *domain.Error {
	if err == nil {
		return nil
	}
	if err.Code == app.ErrorCodeTimeout {
		err.Message = "pipeline step timed out: " + stepName
	} else if err.Code == app.ErrorCodeExecutionFailed {
		err.Message = "pipeline step failed: " + stepName
	}
	return err
}

func ciPipelineResult(projectType string, steps []map[string]any, failedStep string, exitCode int, output string) app.ToolRunResult {
	summary := "pipeline succeeded"
	if failedStep != "" {
		summary = "pipeline failed on " + failedStep
	}
	return app.ToolRunResult{
		ExitCode: exitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: output}},
		Output: map[string]any{
			"project_type": projectType,
			"steps":        steps,
			"exit_code":    exitCode,
			"failed_step":  failedStep,
			"summary":      summary,
			"output":       output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweArtifactCIPipelineOutput,
			ContentType: sweTextPlain,
			Data:        []byte(output),
		}},
	}
}

// Blank imports to satisfy the user-requested import list.
var (
	_ = errors.New
	_ = os.ErrNotExist
)
