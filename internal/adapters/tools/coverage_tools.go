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

type RepoCoverageReportHandler struct {
	runner app.CommandRunner
}

func NewRepoCoverageReportHandler(runner app.CommandRunner) *RepoCoverageReportHandler {
	return &RepoCoverageReportHandler{runner: runner}
}

func (h *RepoCoverageReportHandler) Name() string {
	return "repo.coverage_report"
}

func (h *RepoCoverageReportHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Target string `json:"target"`
	}{}
	if len(args) > 0 && json.Unmarshal(args, &request) != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid repo.coverage_report args",
			Retryable: false,
		}
	}

	runner := ensureRunner(h.runner)
	detected, detectErr := detectProjectTypeForSession(ctx, runner, session)
	if detectErr != nil {
		if errors.Is(detectErr, os.ErrNotExist) {
			return app.ToolRunResult{}, &domain.Error{
				Code:      app.ErrorCodeExecutionFailed,
				Message:   "no supported test toolchain found",
				Retryable: false,
			}
		}
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   detectErr.Error(),
			Retryable: false,
		}
	}

	target := sanitizeTarget(request.Target)
	if target == "" {
		target = "./..."
	}

	coverageSupported := detected.Name == sweEcosystemGo
	coveragePercent := 0.0
	command := []string{}
	output := ""
	exitCode := 0

	if detected.Name == sweEcosystemGo {
		return runGoCoverageReport(ctx, runner, session, detected.Name, target)
	}

	testCommand, testArgs, commandErr := testCommandForProject(session.WorkspacePath, detected, target, nil)
	if commandErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   commandErr.Error(),
			Retryable: false,
		}
	}
	command = append([]string{testCommand}, testArgs...)
	testResult, testErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  testCommand,
		Args:     testArgs,
		MaxBytes: 2 * 1024 * 1024,
	})
	exitCode = testResult.ExitCode
	output = testResult.Output
	if parsed := parseCoveragePercent(testResult.Output); parsed != nil {
		coveragePercent = *parsed
		coverageSupported = true
	}

	result := app.ToolRunResult{
		ExitCode: exitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: output}},
		Output: map[string]any{
			"project_type":       detected.Name,
			"command":            command,
			"coverage_supported": coverageSupported,
			"coverage_percent":   coveragePercent,
			"exit_code":          exitCode,
			"output":             output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweCoverageReportTxt,
			ContentType: sweTextPlain,
			Data:        []byte(output),
		}},
	}
	if testErr != nil {
		return result, toToolError(testErr, output)
	}
	return result, nil
}

func runGoCoverageReport(ctx context.Context, runner app.CommandRunner, session domain.Session, projectType, target string) (app.ToolRunResult, *domain.Error) {
	coverageFile := ".workspace.cover.out"
	command := []string{"go", "test", target, sweCoverProfile, coverageFile, sweCoverModeAtomic}
	testResult, testErr := runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "go",
		Args:     []string{"test", target, sweCoverProfile, coverageFile, sweCoverModeAtomic},
		MaxBytes: 2 * 1024 * 1024,
	})
	output := testResult.Output
	exitCode := testResult.ExitCode
	coveragePercent := 0.0

	if testErr == nil {
		coverResult, coverErr := runner.Run(ctx, session, app.CommandSpec{
			Cwd:      session.WorkspacePath,
			Command:  "go",
			Args:     []string{"tool", "cover", "-func=" + coverageFile},
			MaxBytes: 512 * 1024,
		})
		if strings.TrimSpace(coverResult.Output) != "" {
			output = strings.TrimSpace(output + "\n" + coverResult.Output)
		}
		if parsed := parseCoveragePercent(coverResult.Output); parsed != nil {
			coveragePercent = *parsed
		}
		if parsed := parseCoveragePercent(testResult.Output); parsed != nil && coveragePercent == 0.0 {
			coveragePercent = *parsed
		}
		if coverErr != nil {
			exitCode = coverResult.ExitCode
			_, _ = runner.Run(ctx, session, app.CommandSpec{
				Cwd:      session.WorkspacePath,
				Command:  "rm",
				Args:     []string{"-f", coverageFile},
				MaxBytes: 16 * 1024,
			})
			result := app.ToolRunResult{
				ExitCode: exitCode,
				Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: output}},
				Output: map[string]any{
					"project_type":       projectType,
					"command":            command,
					"coverage_supported": true,
					"coverage_percent":   coveragePercent,
					"exit_code":          exitCode,
					"output":             output,
				},
				Artifacts: []app.ArtifactPayload{{
					Name:        sweCoverageReportTxt,
					ContentType: sweTextPlain,
					Data:        []byte(output),
				}},
			}
			return result, toToolError(coverErr, output)
		}
	}

	_, _ = runner.Run(ctx, session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "rm",
		Args:     []string{"-f", coverageFile},
		MaxBytes: 16 * 1024,
	})

	result := app.ToolRunResult{
		ExitCode: exitCode,
		Logs:     []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: output}},
		Output: map[string]any{
			"project_type":       projectType,
			"command":            command,
			"coverage_supported": true,
			"coverage_percent":   coveragePercent,
			"exit_code":          exitCode,
			"output":             output,
		},
		Artifacts: []app.ArtifactPayload{{
			Name:        sweCoverageReportTxt,
			ContentType: sweTextPlain,
			Data:        []byte(output),
		}},
	}
	if testErr != nil {
		return result, toToolError(testErr, output)
	}
	return result, nil
}
