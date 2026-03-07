//go:build k8s

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

func TestK8sCommandRunner_NilClient(t *testing.T) {
	runner := &K8sCommandRunner{client: nil, restConfig: nil}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", result.ExitCode)
	}
}

func TestK8sCommandRunner_ResolveExecParams_EmptyPodName(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "test-ns"},
	}
	_, _, _, _, err := runner.resolveExecParams(session, app.CommandSpec{})
	if err == nil {
		t.Fatal("expected error for empty pod name")
	}
}

func TestK8sCommandRunner_ResolveExecParams_Defaults(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "fallback-ns")
	session := domain.Session{
		WorkspacePath: "/workspace/repo",
		Runtime: domain.RuntimeRef{
			Kind:    domain.RuntimeKindKubernetes,
			PodName: "ws-1",
			Workdir: "/custom/dir",
		},
	}
	namespace, podName, container, workdir, err := runner.resolveExecParams(session, app.CommandSpec{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if namespace != "fallback-ns" {
		t.Fatalf("expected fallback namespace, got %q", namespace)
	}
	if podName != "ws-1" {
		t.Fatalf("expected ws-1, got %q", podName)
	}
	if container != "runner" {
		t.Fatalf("expected default container 'runner', got %q", container)
	}
	if workdir != "/custom/dir" {
		t.Fatalf("expected runtime workdir, got %q", workdir)
	}
}

func TestK8sCommandRunner_ResolveExecParams_CwdOverridesWorkdir(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "ns")
	session := domain.Session{
		WorkspacePath: "/workspace/repo",
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, PodName: "ws-1", Container: "app", Workdir: "/runtime"},
	}
	_, _, container, workdir, err := runner.resolveExecParams(session, app.CommandSpec{Cwd: "/override"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if container != "app" {
		t.Fatalf("expected custom container 'app', got %q", container)
	}
	if workdir != "/override" {
		t.Fatalf("expected cwd override, got %q", workdir)
	}
}

func TestK8sCommandRunner_BuildExecutorError(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return nil, errors.New("url build failed")
	}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected error from url builder")
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", result.ExitCode)
	}
}

func TestK8sCommandRunner_FactoryBuildError(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return &url.URL{Scheme: "https", Host: "k8s.local"}, nil
	}
	runner.executorFactory = fakeExecutorFactory{err: errors.New("factory failed")}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected error from factory")
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", result.ExitCode)
	}
}

func TestRoutingCommandRunner_NilLocal(t *testing.T) {
	routing := NewRoutingCommandRunner(nil, nil)
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindLocal},
	}
	_, err := routing.Run(context.Background(), session, app.CommandSpec{Command: "echo"})
	if err == nil {
		t.Fatal("expected error for nil local runner")
	}
}

func TestK8sCommandRunner_RunWithStdin(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return &url.URL{Scheme: "https", Host: "k8s.local"}, nil
	}
	runner.executorFactory = fakeExecutorFactory{
		executor: fakeStreamExecutor{stdout: []byte("received")},
	}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{
		Command: "cat",
		Stdin:   []byte("input data"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "received") {
		t.Fatalf("expected 'received' in output, got %q", result.Output)
	}
}

func TestK8sCommandRunner_StreamGenericError(t *testing.T) {
	runner := NewK8sCommandRunner(k8sfake.NewSimpleClientset(), &rest.Config{}, "default")
	runner.execURLBuilder = func(_, _ string, _ *corev1.PodExecOptions) (*url.URL, error) {
		return &url.URL{Scheme: "https", Host: "k8s.local"}, nil
	}
	runner.executorFactory = fakeExecutorFactory{
		executor: fakeStreamExecutor{
			stderr: []byte("oops"),
			err:    errors.New("stream failed"),
		},
	}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "default", PodName: "ws-1"},
	}
	result, err := runner.Run(context.Background(), session, app.CommandSpec{Command: "test"})
	if err == nil {
		t.Fatal("expected stream error")
	}
	if result.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", result.ExitCode)
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
