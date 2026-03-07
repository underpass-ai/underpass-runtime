package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type LocalCommandRunner struct{}

type RoutingCommandRunner struct {
	local      app.CommandRunner
	kubernetes app.CommandRunner
}

type streamExecutor interface {
	StreamWithContext(ctx context.Context, options remotecommand.StreamOptions) error
}

type ExecutorBuilder interface {
	Build(config *rest.Config, method string, url *url.URL) (streamExecutor, error)
}

type defaultExecutorFactory struct{}

type execURLBuilder func(namespace, podName string, options *corev1.PodExecOptions) (*url.URL, error)

type K8sCommandRunner struct {
	client           kubernetes.Interface
	restConfig       *rest.Config
	defaultNamespace string
	executorFactory  ExecutorBuilder
	execURLBuilder   execURLBuilder
}

func NewLocalCommandRunner() *LocalCommandRunner {
	return &LocalCommandRunner{}
}

func NewK8sCommandRunner(
	client kubernetes.Interface,
	restConfig *rest.Config,
	defaultNamespace string,
) *K8sCommandRunner {
	return &K8sCommandRunner{
		client:           client,
		restConfig:       restConfig,
		defaultNamespace: strings.TrimSpace(defaultNamespace),
		executorFactory:  defaultExecutorFactory{},
	}
}

func NewRoutingCommandRunner(local, kubernetes app.CommandRunner) *RoutingCommandRunner {
	return &RoutingCommandRunner{
		local:      local,
		kubernetes: kubernetes,
	}
}

func (defaultExecutorFactory) Build(config *rest.Config, method string, url *url.URL) (streamExecutor, error) {
	return remotecommand.NewSPDYExecutor(config, method, url)
}

func (r *LocalCommandRunner) Run(ctx context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	output, exitCode, err := runCommand(ctx, spec.Cwd, spec.MaxBytes, spec.Stdin, spec.Command, spec.Args...)
	return app.CommandResult{
		Output:   output,
		ExitCode: exitCode,
	}, err
}

func (r *K8sCommandRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if r.client == nil || r.restConfig == nil {
		return app.CommandResult{ExitCode: -1}, fmt.Errorf("kubernetes client and rest config are required")
	}

	namespace, podName, container, workdir, resolveErr := r.resolveExecParams(session, spec)
	if resolveErr != nil {
		return app.CommandResult{ExitCode: -1}, resolveErr
	}

	execOptions := &corev1.PodExecOptions{
		Container: container,
		// Use non-login shell to preserve image PATH/toolchain env (cargo/go/etc.).
		Command:   []string{"sh", "-c", buildShellCommand(workdir, spec.Command, spec.Args)},
		Stdin:     len(spec.Stdin) > 0,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}

	executor, err := r.buildK8sExecutor(namespace, podName, execOptions)
	if err != nil {
		return app.CommandResult{ExitCode: -1}, err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var stdin io.Reader
	if len(spec.Stdin) > 0 {
		stdin = bytes.NewReader(spec.Stdin)
	}

	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})

	output := combineOutput(stdout.Bytes(), stderr.Bytes())
	output = truncate(output, spec.MaxBytes)
	text := string(output)

	if streamErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return app.CommandResult{
				Output:   text,
				ExitCode: 124,
			}, fmt.Errorf("timeout: %w", ctx.Err())
		}
		var exitErr utilexec.ExitError
		if errors.As(streamErr, &exitErr) {
			return app.CommandResult{
				Output:   text,
				ExitCode: exitErr.ExitStatus(),
			}, streamErr
		}
		return app.CommandResult{
			Output:   text,
			ExitCode: -1,
		}, streamErr
	}

	return app.CommandResult{
		Output:   text,
		ExitCode: 0,
	}, nil
}

func (r *K8sCommandRunner) resolveExecParams(session domain.Session, spec app.CommandSpec) (namespace, podName, container, workdir string, err error) {
	namespace = strings.TrimSpace(session.Runtime.Namespace)
	if namespace == "" {
		namespace = r.defaultNamespace
	}
	podName = strings.TrimSpace(session.Runtime.PodName)
	if podName == "" {
		return "", "", "", "", fmt.Errorf("kubernetes runtime pod_name is required")
	}
	container = strings.TrimSpace(session.Runtime.Container)
	if container == "" {
		container = "runner"
	}
	workdir = strings.TrimSpace(spec.Cwd)
	if workdir == "" {
		workdir = strings.TrimSpace(session.Runtime.Workdir)
	}
	if workdir == "" {
		workdir = strings.TrimSpace(session.WorkspacePath)
	}
	return namespace, podName, container, workdir, nil
}

func (r *K8sCommandRunner) buildK8sExecutor(namespace, podName string, execOptions *corev1.PodExecOptions) (streamExecutor, error) {
	urlBuilder := r.execURLBuilder
	if urlBuilder == nil {
		urlBuilder = r.defaultExecURLBuilder()
	}
	execURL, err := urlBuilder(namespace, podName, execOptions)
	if err != nil {
		return nil, err
	}
	factory := r.executorFactory
	if factory == nil {
		factory = defaultExecutorFactory{}
	}
	return factory.Build(r.restConfig, "POST", execURL)
}

func (r *K8sCommandRunner) defaultExecURLBuilder() execURLBuilder {
	return func(namespace, podName string, options *corev1.PodExecOptions) (*url.URL, error) {
		restClient := r.client.CoreV1().RESTClient()
		if restClient == nil {
			return nil, fmt.Errorf("kubernetes rest client is not available")
		}
		request := restClient.Post().
			Resource("pods").
			Name(podName).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(options, scheme.ParameterCodec)
		return request.URL(), nil
	}
}

func (r *RoutingCommandRunner) Run(ctx context.Context, session domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	if session.Runtime.Kind == domain.RuntimeKindKubernetes && r.kubernetes != nil {
		return r.kubernetes.Run(ctx, session, spec)
	}
	if r.local == nil {
		return app.CommandResult{ExitCode: -1}, fmt.Errorf("local command runner is not configured")
	}
	return r.local.Run(ctx, session, spec)
}

func runCommand(ctx context.Context, cwd string, maxBytes int, stdin []byte, command string, args ...string) (string, int, error) {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}

	cmd := exec.CommandContext(ctx, command, args...)
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
