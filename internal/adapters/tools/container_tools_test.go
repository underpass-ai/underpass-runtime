package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type fakeContainerRunner struct {
	calls []app.CommandSpec
	run   func(callIndex int, spec app.CommandSpec) (app.CommandResult, error)
}

func (f *fakeContainerRunner) Run(_ context.Context, _ domain.Session, spec app.CommandSpec) (app.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if f.run != nil {
		return f.run(len(f.calls)-1, spec)
	}
	return app.CommandResult{ExitCode: 0}, nil
}

func TestContainerPSHandler_SimulatedWhenRuntimeUnavailable(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 1, Output: "cannot connect to runtime"}, errors.New("exit status 1")
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerPSHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":25,"strict":false}`))
	if err != nil {
		t.Fatalf("unexpected container.ps error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["simulated"] != true || output["runtime"] != "synthetic" {
		t.Fatalf("expected simulated synthetic output, got %#v", output)
	}
	if output["count"] != 0 {
		t.Fatalf("expected count=0, got %#v", output["count"])
	}
}

func TestContainerPSHandler_RuntimeAndTruncation(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == "podman" && len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 0, Output: "{}"}, nil
			}
			if spec.Command == "podman" && len(spec.Args) > 0 && spec.Args[0] == "ps" {
				return app.CommandResult{ExitCode: 0, Output: "b123\timg-b\tb\trunning\na123\timg-a\ta\texited"}, nil
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerPSHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatalf("unexpected container.ps error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != "podman" || output["simulated"] != false {
		t.Fatalf("expected podman runtime output, got %#v", output)
	}
	if output["count"] != 1 || output["truncated"] != true {
		t.Fatalf("expected count=1 truncated=true, got %#v", output)
	}
	containers := output["containers"].([]map[string]any)
	if len(containers) != 1 || containers[0]["id"] != "a123" {
		t.Fatalf("expected sorted/truncated containers, got %#v", containers)
	}
}

func TestContainerRunHandler_StrictNoRuntimeFails(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 1, Output: "no runtime"}, errors.New("exit status 1")
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerRunHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"image_ref":"busybox:1.36","strict":true}`))
	if err == nil {
		t.Fatal("expected strict runtime failure")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestContainerRunHandler_UsesRuntime(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == "podman" && len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 0, Output: "{}"}, nil
			}
			if spec.Command == "podman" && len(spec.Args) > 0 && spec.Args[0] == "run" {
				if !containsArg(spec.Args, "-d") {
					t.Fatalf("expected detach flag in run args: %#v", spec.Args)
				}
				return app.CommandResult{ExitCode: 0, Output: "abc123def456\n"}, nil
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerRunHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir(), ID: "s1"}, json.RawMessage(`{"image_ref":"busybox:1.36","command":["echo","ok"]}`))
	if err != nil {
		t.Fatalf("unexpected container.run error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != "podman" || output["simulated"] != false {
		t.Fatalf("expected podman runtime output, got %#v", output)
	}
	if output["container_id"] != "abc123def456" {
		t.Fatalf("unexpected container_id: %#v", output["container_id"])
	}
}

func TestContainerLogsHandler_SimulatedID(t *testing.T) {
	handler := NewContainerLogsHandler(nil)
	result, err := handler.Invoke(context.Background(), domain.Session{}, json.RawMessage(`{"container_id":"sim-123456","strict":false}`))
	if err != nil {
		t.Fatalf("unexpected container.logs error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["simulated"] != true {
		t.Fatalf("expected simulated logs output, got %#v", output)
	}
	if !strings.Contains(output["logs"].(string), "simulated logs") {
		t.Fatalf("expected simulated logs text, got %#v", output["logs"])
	}
}

func TestContainerExecHandler_DeniesDisallowedCommand(t *testing.T) {
	handler := NewContainerExecHandler(nil)
	_, err := handler.Invoke(context.Background(), domain.Session{}, json.RawMessage(`{"container_id":"sim-123456","command":["rm","-rf","/"]}`))
	if err == nil {
		t.Fatal("expected command denial")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}

func TestContainerExecHandler_UsesRuntime(t *testing.T) {
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command == "podman" && len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 0, Output: "{}"}, nil
			}
			if spec.Command == "podman" && len(spec.Args) > 0 && spec.Args[0] == "exec" {
				return app.CommandResult{ExitCode: 0, Output: "hello from container"}, nil
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerExecHandler(runner)
	result, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"container_id":"abc123","command":["echo","hello"],"strict":true}`))
	if err != nil {
		t.Fatalf("unexpected container.exec error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != "podman" || output["simulated"] != false {
		t.Fatalf("expected podman runtime output, got %#v", output)
	}
	if !strings.Contains(output["output"].(string), "hello") {
		t.Fatalf("unexpected exec output: %#v", output["output"])
	}
}

func TestContainerRunHandler_UsesKubernetesPodRuntime(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	client.Fake.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		getAction, ok := action.(k8stesting.GetAction)
		if !ok {
			return false, nil, nil
		}
		obj, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), getAction.GetNamespace(), getAction.GetName())
		if err != nil {
			return true, nil, err
		}
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return true, obj, nil
		}
		copy := pod.DeepCopy()
		if copy.Status.Phase == "" {
			copy.Status.Phase = corev1.PodRunning
		}
		return true, copy, nil
	})

	handler := NewContainerRunHandlerWithKubernetes(nil, client, "sandbox")
	session := domain.Session{
		ID:        "session-k8s-run",
		Principal: domain.Principal{TenantID: "tenant-a"},
		Runtime: domain.RuntimeRef{
			Kind:      domain.RuntimeKindKubernetes,
			Namespace: "sandbox",
		},
	}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"image_ref":"busybox:1.36","command":["sleep","5"],"detach":true}`),
	)
	if err != nil {
		t.Fatalf("unexpected container.run k8s error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != "k8s" || output["simulated"] != false {
		t.Fatalf("expected k8s runtime output, got %#v", output)
	}
	containerID := strings.TrimSpace(asString(output["container_id"]))
	if containerID == "" {
		t.Fatalf("expected non-empty container_id, got %#v", output["container_id"])
	}
	if output["namespace"] != "sandbox" {
		t.Fatalf("expected sandbox namespace, got %#v", output["namespace"])
	}

	pod, getErr := client.CoreV1().Pods("sandbox").Get(context.Background(), containerID, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected created pod %s: %v", containerID, getErr)
	}
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != "task" {
		t.Fatalf("unexpected pod container spec: %#v", pod.Spec.Containers)
	}
	if pod.Spec.Containers[0].Image != "busybox:1.36" {
		t.Fatalf("unexpected pod image: %#v", pod.Spec.Containers[0].Image)
	}
	if strings.Join(pod.Spec.Containers[0].Command, " ") != "sleep 5" {
		t.Fatalf("unexpected pod command: %#v", pod.Spec.Containers[0].Command)
	}
}

func TestContainerPSHandler_UsesKubernetesPodRuntime(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ws-ctr-alpha",
				Namespace: "sandbox",
				Labels: map[string]string{
					"app":                  "workspace-container-run",
					"workspace_session_id": "session-k8s-ps",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: "busybox:1.36"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ws-ctr-bravo",
				Namespace: "sandbox",
				Labels: map[string]string{
					"app":                  "workspace-container-run",
					"workspace_session_id": "session-k8s-ps",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: "busybox:1.36"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ws-ctr-other",
				Namespace: "sandbox",
				Labels: map[string]string{
					"app":                  "workspace-container-run",
					"workspace_session_id": "other-session",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: "busybox:1.36"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	handler := NewContainerPSHandlerWithKubernetes(nil, client, "sandbox")
	session := domain.Session{
		ID: "session-k8s-ps",
		Runtime: domain.RuntimeRef{
			Kind:      domain.RuntimeKindKubernetes,
			Namespace: "sandbox",
		},
	}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"all":false,"limit":20,"strict":true}`),
	)
	if err != nil {
		t.Fatalf("unexpected container.ps k8s error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != "k8s" || output["simulated"] != false {
		t.Fatalf("expected k8s runtime output, got %#v", output)
	}
	if output["count"] != 1 {
		t.Fatalf("expected one running container for session, got %#v", output["count"])
	}
	containers, ok := output["containers"].([]map[string]any)
	if !ok {
		t.Fatalf("expected []map output containers, got %#v", output["containers"])
	}
	if len(containers) != 1 || containers[0]["id"] != "ws-ctr-alpha" {
		t.Fatalf("unexpected containers list: %#v", containers)
	}
	if containers[0]["status"] != "running" {
		t.Fatalf("unexpected container status: %#v", containers[0]["status"])
	}
}

func TestContainerExecHandler_UsesKubernetesPodRuntime(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ctr-pod", Namespace: "sandbox"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: "busybox:1.36"}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
	)
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if spec.Command != "echo" || len(spec.Args) != 1 || spec.Args[0] != "ok" {
				t.Fatalf("unexpected exec command spec: %#v", spec)
			}
			if spec.Cwd != "" {
				t.Fatalf("expected empty cwd for k8s exec, got %#v", spec.Cwd)
			}
			return app.CommandResult{ExitCode: 0, Output: "ok"}, nil
		},
	}
	handler := NewContainerExecHandlerWithKubernetes(runner, client, "sandbox")
	session := domain.Session{
		WorkspacePath: "/tmp/workspace",
		Runtime: domain.RuntimeRef{
			Kind:      domain.RuntimeKindKubernetes,
			Namespace: "sandbox",
		},
	}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"container_id":"ctr-pod","command":["echo","ok"],"strict":true}`),
	)
	if err != nil {
		t.Fatalf("unexpected container.exec k8s error: %#v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected one runner call, got %d", len(runner.calls))
	}
	output := result.Output.(map[string]any)
	if output["runtime"] != "k8s" || output["simulated"] != false {
		t.Fatalf("expected k8s runtime output, got %#v", output)
	}
	if output["pod_name"] != "ctr-pod" {
		t.Fatalf("unexpected pod_name: %#v", output["pod_name"])
	}
	if !strings.Contains(output["output"].(string), "ok") {
		t.Fatalf("unexpected exec output: %#v", output["output"])
	}
}

func TestContainerPSHandler_StrictByDefaultEnvFailsWithoutRuntime(t *testing.T) {
	t.Setenv("WORKSPACE_CONTAINER_STRICT_BY_DEFAULT", "true")
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 1, Output: "cannot connect to runtime"}, errors.New("exit status 1")
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerPSHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":25}`))
	if err == nil {
		t.Fatal("expected strict-by-default runtime failure")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestContainerPSHandler_SyntheticFallbackDisabledEnvForcesStrict(t *testing.T) {
	t.Setenv("WORKSPACE_CONTAINER_ALLOW_SYNTHETIC_FALLBACK", "false")
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 1, Output: "cannot connect to runtime"}, errors.New("exit status 1")
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerPSHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"limit":25,"strict":false}`))
	if err == nil {
		t.Fatal("expected runtime failure when synthetic fallback disabled")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestContainerLogsHandler_SyntheticFallbackDisabledEnvForcesStrict(t *testing.T) {
	t.Setenv("WORKSPACE_CONTAINER_ALLOW_SYNTHETIC_FALLBACK", "false")
	runner := &fakeContainerRunner{
		run: func(_ int, spec app.CommandSpec) (app.CommandResult, error) {
			if len(spec.Args) > 0 && spec.Args[0] == "info" {
				return app.CommandResult{ExitCode: 1, Output: "cannot connect to runtime"}, errors.New("exit status 1")
			}
			return app.CommandResult{ExitCode: 1}, errors.New("unexpected command")
		},
	}

	handler := NewContainerLogsHandler(runner)
	_, err := handler.Invoke(context.Background(), domain.Session{WorkspacePath: t.TempDir()}, json.RawMessage(`{"container_id":"sim-123456","strict":false}`))
	if err == nil {
		t.Fatal("expected runtime failure when synthetic fallback disabled")
	}
	if err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestContainerExecHandler_DeniesShellCommands(t *testing.T) {
	handler := NewContainerExecHandler(nil)
	_, err := handler.Invoke(context.Background(), domain.Session{}, json.RawMessage(`{"container_id":"sim-123456","command":["sh","-c","echo hello"]}`))
	if err == nil {
		t.Fatal("expected shell command denial")
	}
	if err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}

func TestContainerHandlerNames(t *testing.T) {
	if NewContainerPSHandler(nil).Name() != "container.ps" {
		t.Fatal("unexpected container.ps name")
	}
	if NewContainerLogsHandler(nil).Name() != "container.logs" {
		t.Fatal("unexpected container.logs name")
	}
	if NewContainerRunHandler(nil).Name() != "container.run" {
		t.Fatal("unexpected container.run name")
	}
	if NewContainerExecHandler(nil).Name() != "container.exec" {
		t.Fatal("unexpected container.exec name")
	}
}

func TestBuildSimulatedContainerID(t *testing.T) {
	id1 := buildSimulatedContainerID("sess1", "busybox:latest", []string{"echo", "hi"}, "mycontainer")
	id2 := buildSimulatedContainerID("sess1", "busybox:latest", []string{"echo", "hi"}, "mycontainer")
	id3 := buildSimulatedContainerID("sess2", "busybox:latest", []string{"echo", "hi"}, "mycontainer")

	if id1 != id2 {
		t.Fatalf("expected same inputs to produce same ID: %q != %q", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("expected different inputs to produce different IDs, both got %q", id1)
	}
	if !strings.HasPrefix(id1, "sim-") {
		t.Fatalf("expected ID to start with 'sim-', got %q", id1)
	}
	// result is "sim-" + 12 hex chars = 16 chars total
	if len(id1) != 16 {
		t.Fatalf("expected ID length 16, got %d: %q", len(id1), id1)
	}
}

func TestBuildContainerLogsCommand(t *testing.T) {
	// Without sinceSec and without timestamps
	cmd := buildContainerLogsCommand("docker", "ctr-1", 50, 0, false)
	expected := []string{"docker", "logs", "--tail", "50", "ctr-1"}
	if len(cmd) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, cmd)
	}
	for i, v := range expected {
		if cmd[i] != v {
			t.Fatalf("expected cmd[%d]=%q, got %q", i, v, cmd[i])
		}
	}

	// With sinceSec=30 and timestamps=true
	cmd2 := buildContainerLogsCommand("docker", "ctr-1", 50, 30, true)
	if !containsArg(cmd2, "--since") {
		t.Fatalf("expected --since in cmd: %v", cmd2)
	}
	if !containsArg(cmd2, "30s") {
		t.Fatalf("expected 30s in cmd: %v", cmd2)
	}
	if !containsArg(cmd2, "--timestamps") {
		t.Fatalf("expected --timestamps in cmd: %v", cmd2)
	}
}

func TestFirstTerminatedContainerStatus(t *testing.T) {
	// Empty ContainerStatuses -> returns false
	emptyStatus := corev1.PodStatus{}
	_, ok := firstTerminatedContainerStatus(emptyStatus)
	if ok {
		t.Fatal("expected false for empty ContainerStatuses")
	}

	// ContainerStatuses with a Terminated state -> returns true and ExitCode
	exitCode := int32(42)
	statusWithTerminated := corev1.PodStatus{
		ContainerStatuses: []corev1.ContainerStatus{
			{
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: exitCode,
					},
				},
			},
		},
	}
	terminated, ok2 := firstTerminatedContainerStatus(statusWithTerminated)
	if !ok2 {
		t.Fatal("expected true for ContainerStatuses with Terminated state")
	}
	if terminated.ExitCode != exitCode {
		t.Fatalf("expected ExitCode=%d, got %d", exitCode, terminated.ExitCode)
	}
}

func TestShouldFallbackToContainerSimulation(t *testing.T) {
	// Empty string -> false
	if shouldFallbackToContainerSimulation("") {
		t.Fatal("expected false for empty string")
	}

	// Known error pattern -> true
	if !shouldFallbackToContainerSimulation("cannot connect to the docker daemon") {
		t.Fatal("expected true for docker daemon connection error")
	}

	// Another known pattern -> true
	if !shouldFallbackToContainerSimulation("connection refused while doing something") {
		t.Fatal("expected true for connection refused output")
	}

	// Healthy runtime output -> false
	if shouldFallbackToContainerSimulation("healthy runtime output OK") {
		t.Fatal("expected false for healthy runtime output")
	}
}

func TestSanitizeContainerEnv(t *testing.T) {
	// nil/empty map -> empty slice, no error
	pairs, err := sanitizeContainerEnv(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil map: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected empty slice for nil map, got %v", pairs)
	}

	pairs, err = sanitizeContainerEnv(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error for empty map: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected empty slice for empty map, got %v", pairs)
	}

	// Valid key=value pairs -> sorted KEY=value pairs
	pairs, err = sanitizeContainerEnv(map[string]string{
		"Z_VAR": "zval",
		"A_VAR": "aval",
	})
	if err != nil {
		t.Fatalf("unexpected error for valid env: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d: %v", len(pairs), pairs)
	}
	if pairs[0] != "A_VAR=aval" {
		t.Fatalf("expected first pair to be A_VAR=aval, got %q", pairs[0])
	}
	if pairs[1] != "Z_VAR=zval" {
		t.Fatalf("expected second pair to be Z_VAR=zval, got %q", pairs[1])
	}

	// Key with invalid characters (starts with digit) -> error
	_, err = sanitizeContainerEnv(map[string]string{"123bad": "value"})
	if err == nil {
		t.Fatal("expected error for key starting with digit")
	}

	// Value with newline -> error
	_, err = sanitizeContainerEnv(map[string]string{"GOOD_KEY": "bad\nvalue"})
	if err == nil {
		t.Fatal("expected error for value with newline")
	}

	// More than 32 entries -> error
	tooMany := make(map[string]string, containerMaxRunEnvVars+1)
	for i := 0; i <= containerMaxRunEnvVars; i++ {
		tooMany[fmt.Sprintf("KEY_%d", i)] = "val"
	}
	_, err = sanitizeContainerEnv(tooMany)
	if err == nil {
		t.Fatal("expected error for too many env vars")
	}

	// Value longer than 256 bytes -> error
	longVal := strings.Repeat("x", containerMaxCommandArgLength+1)
	_, err = sanitizeContainerEnv(map[string]string{"MY_KEY": longVal})
	if err == nil {
		t.Fatal("expected error for value exceeding max length")
	}
}

func TestResolveK8sRunContainerName(t *testing.T) {
	// nil pod -> "task"
	if name := resolveK8sRunContainerName(nil); name != "task" {
		t.Fatalf("expected 'task' for nil pod, got %q", name)
	}

	// Pod with container named "task" -> "task"
	podWithTask := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "task"},
			},
		},
	}
	if name := resolveK8sRunContainerName(podWithTask); name != "task" {
		t.Fatalf("expected 'task', got %q", name)
	}

	// Pod with no containers -> "task"
	podEmpty := &corev1.Pod{
		Spec: corev1.PodSpec{},
	}
	if name := resolveK8sRunContainerName(podEmpty); name != "task" {
		t.Fatalf("expected 'task' for pod with no containers, got %q", name)
	}

	// Pod with one container named "runner" -> "runner"
	podWithRunner := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "runner"},
			},
		},
	}
	if name := resolveK8sRunContainerName(podWithRunner); name != "runner" {
		t.Fatalf("expected 'runner', got %q", name)
	}
}

func TestWaitForK8sContainerPodTerminal_AlreadyTerminated(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	client := k8sfake.NewSimpleClientset(pod)
	result, err := waitForK8sContainerPodTerminal(context.Background(), client, "default", "test-pod", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status.Phase != corev1.PodSucceeded {
		t.Fatalf("expected Succeeded, got %v", result.Status.Phase)
	}
}

func TestWaitForK8sContainerPodTerminal_AlreadyFailed(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "failed-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}
	client := k8sfake.NewSimpleClientset(pod)
	result, err := waitForK8sContainerPodTerminal(context.Background(), client, "default", "failed-pod", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status.Phase != corev1.PodFailed {
		t.Fatalf("expected Failed, got %v", result.Status.Phase)
	}
}

func TestNewContainerLogsHandlerWithKubernetes(t *testing.T) {
	runner := &fakeContainerRunner{}
	client := k8sfake.NewSimpleClientset()
	h := NewContainerLogsHandlerWithKubernetes(runner, client, "  test-ns  ")
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.defaultNamespace != "test-ns" {
		t.Fatalf("expected defaultNamespace='test-ns', got %q", h.defaultNamespace)
	}
	if h.client == nil {
		t.Fatal("expected k8s client to be set")
	}
}

func TestBuildSimulatedContainerRunResult_Detach(t *testing.T) {
	opts := simulatedContainerRunOptions{
		sessionID:     "sess1",
		imageRef:      "nginx:latest",
		containerName: "mycontainer",
		command:       []string{"nginx"},
		envPairs:      []string{"FOO=bar"},
		detach:        true,
		remove:        false,
		outputText:    "some output",
	}
	result := buildSimulatedContainerRunResult(opts)
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "running" {
		t.Fatalf("expected status='running' for detach=true, got %q", output[containerKeyStatus])
	}
	if output[containerSourceSimulated] != true {
		t.Fatal("expected simulated=true")
	}
	if output["image_ref"] != "nginx:latest" {
		t.Fatalf("expected image_ref='nginx:latest', got %q", output["image_ref"])
	}
}

func TestBuildSimulatedContainerRunResult_NonDetach(t *testing.T) {
	opts := simulatedContainerRunOptions{
		sessionID:     "sess2",
		imageRef:      "alpine:3",
		containerName: "task",
		command:       []string{"echo", "hi"},
		detach:        false,
		outputText:    "hi",
	}
	result := buildSimulatedContainerRunResult(opts)
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "exited" {
		t.Fatalf("expected status='exited' for detach=false, got %q", output[containerKeyStatus])
	}
}

func TestHandleContainerRunError_NonStrict(t *testing.T) {
	cmdResult := app.CommandResult{ExitCode: 1, Output: "pull failed"}
	result, domErr := handleContainerRunError(
		containerRunContext{
			sessionID: "sess1", imageRef: "nginx:latest", containerName: "mybox",
			command: []string{"nginx"}, envPairs: []string{},
			detach: false, remove: true, strict: false,
		},
		"docker", cmdResult, errors.New("exit 1"),
	)
	if domErr != nil {
		t.Fatalf("expected nil error for non-strict mode, got %v", domErr)
	}
	output := result.Output.(map[string]any)
	if output[containerSourceSimulated] != true {
		t.Fatal("expected simulated=true in non-strict mode")
	}
}

func TestHandleContainerRunError_Strict(t *testing.T) {
	cmdResult := app.CommandResult{ExitCode: 1, Output: "docker error"}
	result, domErr := handleContainerRunError(
		containerRunContext{
			sessionID: "sess1", imageRef: "nginx:latest", containerName: "mybox",
			command: []string{"nginx"}, envPairs: []string{},
			detach: false, remove: true, strict: true,
		},
		"docker", cmdResult, errors.New("exit 1"),
	)
	if domErr == nil {
		t.Fatal("expected domain error for strict mode")
	}
	output := result.Output.(map[string]any)
	if output[containerKeyStatus] != "failed" {
		t.Fatalf("expected status='failed' for strict mode error, got %q", output[containerKeyStatus])
	}
	if output[containerSourceSimulated] != false {
		t.Fatal("expected simulated=false in strict mode error")
	}
}

func TestInvokeK8sLogs_PodNotFound(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	runner := &fakeContainerRunner{}
	h := NewContainerLogsHandlerWithKubernetes(runner, client, "default")
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := h.Invoke(context.Background(), session, json.RawMessage(`{"container_id":"nosuchpod1"}`))
	if domErr == nil {
		t.Fatal("expected not_found error for missing pod")
	}
	if domErr.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not_found error code, got %q", domErr.Code)
	}
}

func TestInvokeK8sLogs_PodExists_ReturnsOutput(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mypod123", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "task"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	client := k8sfake.NewSimpleClientset(pod)
	runner := &fakeContainerRunner{}
	h := NewContainerLogsHandlerWithKubernetes(runner, client, "default")
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	result, domErr := h.Invoke(context.Background(), session, json.RawMessage(`{"container_id":"mypod123"}`))
	if domErr != nil {
		t.Fatalf("unexpected error: %v", domErr)
	}
	output := result.Output.(map[string]any)
	if output["pod_name"] != "mypod123" {
		t.Fatalf("expected pod_name='mypod123', got %q", output["pod_name"])
	}
	if output["container"] != "task" {
		t.Fatalf("expected container='task', got %q", output["container"])
	}
	if output["source"] != "k8s_sdk" {
		t.Fatalf("expected source='k8s_sdk', got %q", output["source"])
	}
}

func TestInvokeK8sLogs_GenericFetchError(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	// Inject a reactor that returns a generic (non-NotFound) error for Get
	client.PrependReactor("get", "pods", func(_ k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, fmt.Errorf("internal server error")
	})
	runner := &fakeContainerRunner{}
	h := NewContainerLogsHandlerWithKubernetes(runner, client, "default")
	session := domain.Session{
		WorkspacePath: t.TempDir(),
		Runtime:       domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := h.Invoke(context.Background(), session, json.RawMessage(`{"container_id":"anypod1"}`))
	if domErr == nil {
		t.Fatal("expected error when pod Get fails with generic error")
	}
	if domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %q", domErr.Code)
	}
}
