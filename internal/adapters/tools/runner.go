package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type LocalCommandRunner struct{}

func NewLocalCommandRunner() *LocalCommandRunner {
	return &LocalCommandRunner{}
}

func (r *LocalCommandRunner) Run(ctx context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	output, exitCode, err := runCommand(ctx, spec.Cwd, spec.MaxBytes, spec.Stdin, spec.Command, spec.Args...)
	return app.CommandResult{
		Output:   output,
		ExitCode: exitCode,
	}, err
}

func runCommand(ctx context.Context, cwd string, maxBytes int, stdin []byte, command string, args ...string) (string, int, error) {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}

	cmd := exec.CommandContext(ctx, command, args...) //nolint:gosec // codeql[go/command-injection]: command execution is the core purpose of the workspace runtime; inputs are validated by the policy engine before reaching this point
	cmd.Dir = cwd
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	output, err := cmd.CombinedOutput()
	output = truncate(output, maxBytes)
	text := string(output)

	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return text, 124, fmt.Errorf("timeout: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return text, exitErr.ExitCode(), err
		}
		return text, -1, err
	}
	return text, 0, nil
}

func truncate(data []byte, maxBytes int) []byte {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	if len(data) <= maxBytes {
		return data
	}
	trimmed := make([]byte, 0, maxBytes+32)
	trimmed = append(trimmed, data[:maxBytes]...)
	trimmed = append(trimmed, []byte("\n[output truncated]\n")...)
	return trimmed
}

func combineOutput(stdout, stderr []byte) []byte {
	if len(stderr) == 0 {
		return append([]byte(nil), stdout...)
	}
	combined := append([]byte(nil), stdout...)
	if len(combined) > 0 && combined[len(combined)-1] != '\n' {
		combined = append(combined, '\n')
	}
	combined = append(combined, stderr...)
	return combined
}

func buildShellCommand(cwd, command string, args []string) string {
	parts := []string{"exec", shellQuote(command)}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	execCommand := strings.Join(parts, " ")
	if strings.TrimSpace(cwd) == "" {
		return execCommand
	}
	return "cd " + shellQuote(cwd) + " && " + execCommand
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
