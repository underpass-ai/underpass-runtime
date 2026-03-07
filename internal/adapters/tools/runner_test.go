package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestRunCommand_SuccessAndFailure(t *testing.T) {
	output, exitCode, err := runCommand(context.Background(), t.TempDir(), 1024, nil, "sh", "-c", "echo ok")
	if err != nil {
		t.Fatalf("unexpected success error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("unexpected exit code: %d", exitCode)
	}
	if !strings.Contains(output, "ok") {
		t.Fatalf("unexpected output: %q", output)
	}

	output, exitCode, err = runCommand(context.Background(), t.TempDir(), 1024, nil, "sh", "-c", "echo fail; exit 7")
	if err == nil {
		t.Fatal("expected command failure")
	}
	if exitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", exitCode)
	}
	if !strings.Contains(output, "fail") {
		t.Fatalf("unexpected error output: %q", output)
	}
}

func TestRunCommand_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, exitCode, err := runCommand(ctx, t.TempDir(), 1024, nil, "sh", "-c", "sleep 1")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if exitCode != 124 {
		t.Fatalf("expected timeout exit code 124, got %d", exitCode)
	}
}

func TestTruncate(t *testing.T) {
	input := []byte("abcdefghijklmnopqrstuvwxyz")
	result := truncate(input, 5)
	if !strings.Contains(string(result), "[output truncated]") {
		t.Fatalf("expected truncation suffix in %q", string(result))
	}
}

func TestLocalCommandRunner_Run(t *testing.T) {
	runner := NewLocalCommandRunner()
	session := domain.Session{WorkspacePath: t.TempDir()}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "sh",
		Args:     []string{"-c", "echo runner-ok"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if !strings.Contains(result.Output, "runner-ok") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestBuildShellCommand(t *testing.T) {
	command := buildShellCommand("/workspace/repo", "git", []string{"status", "--short"})
	if !strings.Contains(command, "cd '/workspace/repo'") {
		t.Fatalf("expected command to include cwd change, got %q", command)
	}
	if !strings.Contains(command, "exec 'git' 'status' '--short'") {
		t.Fatalf("expected command to include escaped exec, got %q", command)
	}
}
