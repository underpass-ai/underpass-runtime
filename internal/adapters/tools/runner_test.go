package tools

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type fakeStreamExecutor struct {
	stdout []byte
	stderr []byte
	err    error
}

func (f fakeStreamExecutor) StreamWithContext(_ context.Context, options remotecommand.StreamOptions) error {
	if options.Stdout != nil && len(f.stdout) > 0 {
		_, _ = options.Stdout.Write(f.stdout)
	}
	if options.Stderr != nil && len(f.stderr) > 0 {
		_, _ = options.Stderr.Write(f.stderr)
	}
	return f.err
}

type fakeExecutorFactory struct {
	executor streamExecutor
	err      error
}

func (f fakeExecutorFactory) Build(_ *rest.Config, _ string, _ *url.URL) (streamExecutor, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.executor, nil
}

type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string {
	return "command failed"
}

func (e fakeExitError) String() string {
	return e.Error()
}

func (e fakeExitError) Exited() bool {
	return true
}

func (e fakeExitError) ExitStatus() int {
	return e.code
}

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

func TestRoutingCommandRunner_UsesLocalByDefault(t *testing.T) {
	local := NewLocalCommandRunner()
	routing := NewRoutingCommandRunner(local, nil)
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindLocal},
	}
	result, err := routing.Run(context.Background(), session, app.CommandSpec{
		Cwd:      session.WorkspacePath,
		Command:  "sh",
		Args:     []string{"-c", "echo routed-local"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("unexpected routed runner error: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Output, "routed-local") {
		t.Fatalf("unexpected routed result: %#v", result)
	}
}

func TestK8sCommandRunner_Run(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_ string, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return &url.URL{Scheme: "https", Host: "kubernetes.local"}, nil
	}
	runner.executorFactory = fakeExecutorFactory{
		executor: fakeStreamExecutor{
			stdout: []byte("ok"),
			stderr: []byte("warn"),
		},
	}

	session := domain.Session{
		Runtime: domain.RuntimeRef{
			Kind:      domain.RuntimeKindKubernetes,
			Namespace: "default",
			PodName:   "ws-1",
			Container: "runner",
			Workdir:   "/workspace/repo",
		},
	}

	result, err := runner.Run(context.Background(), session, app.CommandSpec{
		Command:  "git",
		Args:     []string{"status"},
		MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Output, "ok") || !strings.Contains(result.Output, "warn") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestK8sCommandRunner_RunExitError(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_ string, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return &url.URL{Scheme: "https", Host: "kubernetes.local"}, nil
	}
	runner.executorFactory = fakeExecutorFactory{
		executor: fakeStreamExecutor{
			stderr: []byte("failed"),
			err:    fakeExitError{code: 12},
		},
	}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}

	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "go", Args: []string{"test"}})
	if err == nil {
		t.Fatal("expected exit error")
	}
	if result.ExitCode != 12 {
		t.Fatalf("expected mapped exit code 12, got %d", result.ExitCode)
	}
}

func TestK8sCommandRunner_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond)

	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_ string, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return &url.URL{Scheme: "https", Host: "kubernetes.local"}, nil
	}
	runner.executorFactory = fakeExecutorFactory{
		executor: fakeStreamExecutor{
			err: context.DeadlineExceeded,
		},
	}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}

	result, err := runner.Run(ctx, session, app.CommandSpec{Command: "go", Args: []string{"test"}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if result.ExitCode != 124 {
		t.Fatalf("expected timeout exit code 124, got %d", result.ExitCode)
	}
}
