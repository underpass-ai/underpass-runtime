//go:build k8s

package tools

import (
	"context"
	"encoding/json"
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

const (
	testK8sContainerImageBusybox136 = "busybox:1.36"
	testK8sContainerAppLabel        = "workspace-container-run"
	testK8sContainerSessionPS       = "session-k8s-ps"
	testK8sRuntimeOutputFmt         = "expected k8s runtime output, got %#v"
)

func TestContainerRunHandler_UsesKubernetesPodRuntime(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	client.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
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
		t.Fatalf(testK8sRuntimeOutputFmt, output)
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
	if pod.Spec.Containers[0].Image != testK8sContainerImageBusybox136 {
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
					"app":                  testK8sContainerAppLabel,
					"workspace_session_id": testK8sContainerSessionPS,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: testK8sContainerImageBusybox136}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ws-ctr-bravo",
				Namespace: "sandbox",
				Labels: map[string]string{
					"app":                  testK8sContainerAppLabel,
					"workspace_session_id": testK8sContainerSessionPS,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: testK8sContainerImageBusybox136}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ws-ctr-other",
				Namespace: "sandbox",
				Labels: map[string]string{
					"app":                  testK8sContainerAppLabel,
					"workspace_session_id": "other-session",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "task", Image: testK8sContainerImageBusybox136}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	handler := NewContainerPSHandlerWithKubernetes(nil, client, "sandbox")
	session := domain.Session{
		ID: testK8sContainerSessionPS,
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
		t.Fatalf(testK8sRuntimeOutputFmt, output)
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
				Containers: []corev1.Container{{Name: "task", Image: testK8sContainerImageBusybox136}},
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
		t.Fatalf(testK8sRuntimeOutputFmt, output)
	}
	if output["pod_name"] != "ctr-pod" {
		t.Fatalf("unexpected pod_name: %#v", output["pod_name"])
	}
	if !strings.Contains(output["output"].(string), "ok") {
		t.Fatalf("unexpected exec output: %#v", output["output"])
	}
}

func TestFirstTerminatedContainerStatus(t *testing.T) {
	emptyStatus := corev1.PodStatus{}
	_, ok := firstTerminatedContainerStatus(emptyStatus)
	if ok {
		t.Fatal("expected false for empty ContainerStatuses")
	}

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

func TestResolveK8sRunContainerName(t *testing.T) {
	if name := resolveK8sRunContainerName(nil); name != "task" {
		t.Fatalf("expected 'task' for nil pod, got %q", name)
	}

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

	podEmpty := &corev1.Pod{
		Spec: corev1.PodSpec{},
	}
	if name := resolveK8sRunContainerName(podEmpty); name != "task" {
		t.Fatalf("expected 'task' for pod with no containers, got %q", name)
	}

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
		t.Fatalf(testUnexpectedErrorFmt, err)
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
		t.Fatalf(testUnexpectedErrorFmt, err)
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
	if h.k8sOps == nil {
		t.Fatal("expected k8s ops to be set")
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
		t.Fatalf(testUnexpectedErrorFmt, domErr)
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

func TestBuildK8sRunContainer_WithEnvPairs(t *testing.T) {
	envPairs := []string{"FOO=bar", "BAZ=qux", "INVALID_NO_EQUALS"}
	c := buildK8sRunContainer("alpine:3.18", []string{"echo", "hello"}, envPairs)
	if c.Name != "task" {
		t.Fatalf("expected container name 'task', got %q", c.Name)
	}
	if c.Image != "alpine:3.18" {
		t.Fatalf("expected image 'alpine:3.18', got %q", c.Image)
	}
	if len(c.Env) != 2 {
		t.Fatalf("expected 2 env vars (INVALID skipped), got %d", len(c.Env))
	}
	if c.Env[0].Name != "FOO" || c.Env[0].Value != "bar" {
		t.Fatalf("unexpected env[0]: %#v", c.Env[0])
	}
	if len(c.Command) != 2 || c.Command[0] != "echo" {
		t.Fatalf("unexpected command: %#v", c.Command)
	}
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("expected AllowPrivilegeEscalation=false")
	}
}

func TestBuildK8sRunContainer_NoCommand(t *testing.T) {
	c := buildK8sRunContainer("alpine:3.18", nil, nil)
	if len(c.Command) != 0 {
		t.Fatalf("expected no command override, got %#v", c.Command)
	}
	if len(c.Env) != 0 {
		t.Fatalf("expected no env vars, got %d", len(c.Env))
	}
}

func TestResolveK8sPodImage_EmptyContainers(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{}}
	if img := resolveK8sPodImage(pod); img != "" {
		t.Fatalf("expected empty image for pod with no containers, got %q", img)
	}
}

func TestResolveK8sPodImage_FallbackToFirst(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "sidecar", Image: "nginx:1.25"},
			},
		},
	}
	if img := resolveK8sPodImage(pod); img != "nginx:1.25" {
		t.Fatalf("expected fallback to first container image, got %q", img)
	}
}

func TestBuildK8sPodLabels_WithTenant(t *testing.T) {
	session := domain.Session{
		ID:        "sess-labels",
		Principal: domain.Principal{TenantID: "  acme  "},
	}
	labels := buildK8sPodLabels(session)
	if labels["workspace_tenant"] != "acme" {
		t.Fatalf("expected trimmed tenant label, got %q", labels["workspace_tenant"])
	}
	if labels["app"] != "workspace-container-run" {
		t.Fatalf("expected app label, got %q", labels["app"])
	}
}

func TestBuildK8sPodLabels_NoTenant(t *testing.T) {
	session := domain.Session{ID: "sess-no-tenant"}
	labels := buildK8sPodLabels(session)
	if _, ok := labels["workspace_tenant"]; ok {
		t.Fatal("expected no tenant label for empty tenant")
	}
}

func TestBuildK8sRunPodName_LongName(t *testing.T) {
	longName := strings.Repeat("a", 100)
	name := buildK8sRunPodName("session-1", longName, "alpine", []string{"sh"})
	if len(name) > 63 {
		t.Fatalf("pod name exceeds 63 chars: %d", len(name))
	}
	if !strings.HasPrefix(name, "ws-ctr-") {
		t.Fatalf("expected ws-ctr- prefix, got %q", name)
	}
}

func TestBuildK8sRunPodName_EmptySession(t *testing.T) {
	name := buildK8sRunPodName("", "", "alpine", nil)
	if !strings.HasPrefix(name, "ws-ctr") {
		t.Fatalf("expected ws-ctr prefix, got %q", name)
	}
}

func TestWaitForK8sPodCompletion_Detach(t *testing.T) {
	status, exitCode, err := waitForK8sPodCompletion(context.Background(), nil, podCompletionConfig{
		detach:   true,
		status:   "running",
		exitCode: 0,
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if status != "running" || exitCode != 0 {
		t.Fatalf("expected running/0, got %s/%d", status, exitCode)
	}
}

func TestWaitForK8sPodCompletion_WithRemove(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rm-pod", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 7}}},
			},
		},
	}
	client := k8sfake.NewSimpleClientset(pod)
	status, exitCode, err := waitForK8sPodCompletion(context.Background(), client, podCompletionConfig{
		namespace: "default",
		podName:   "rm-pod",
		remove:    true,
		status:    "running",
		exitCode:  0,
	})
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if status != "succeeded" {
		t.Fatalf("expected succeeded, got %s", status)
	}
	if exitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", exitCode)
	}
}

func TestContainerPSHandler_AllWithNameFilterAndTruncation(t *testing.T) {
	pods := make([]k8sruntime.Object, 0, 5)
	for i := 0; i < 5; i++ {
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("ws-ctr-item-%d", i),
				Namespace: "sandbox",
				Labels:    map[string]string{"app": testK8sContainerAppLabel, "workspace_session_id": "sess-trunc"},
			},
			Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "task", Image: testK8sContainerImageBusybox136}}},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
		})
	}
	client := k8sfake.NewSimpleClientset(pods...)
	handler := NewContainerPSHandlerWithKubernetes(nil, client, "sandbox")
	session := domain.Session{
		ID:      "sess-trunc",
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes, Namespace: "sandbox"},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"all":true,"limit":3,"name_filter":"item","strict":true}`))
	if err != nil {
		t.Fatalf("unexpected ps k8s error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 3 {
		t.Fatalf("expected truncated to 3, got %#v", output["count"])
	}
	if output["truncated"] != true {
		t.Fatalf("expected truncated=true")
	}
	if output["all"] != true {
		t.Fatalf("expected all=true")
	}
}

func TestContainerExecHandler_PodNotFound(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewContainerExecHandlerWithKubernetes(nil, client, "default")
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := handler.Invoke(context.Background(), session, json.RawMessage(`{"container_id":"missing-pod","command":["echo"],"strict":true}`))
	if domErr == nil {
		t.Fatal("expected not_found error")
	}
	if domErr.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not_found, got %q", domErr.Code)
	}
}

func TestContainerExecHandler_PodNotRunning(t *testing.T) {
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "stopped-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "task", Image: "alpine"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	})
	handler := NewContainerExecHandlerWithKubernetes(nil, client, "default")
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := handler.Invoke(context.Background(), session, json.RawMessage(`{"container_id":"stopped-pod","command":["echo"],"strict":true}`))
	if domErr == nil {
		t.Fatal("expected error for non-running pod")
	}
	if domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %q", domErr.Code)
	}
}

func TestContainerExecHandler_GenericGetError(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	client.PrependReactor("get", "pods", func(_ k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, fmt.Errorf("server error")
	})
	handler := NewContainerExecHandlerWithKubernetes(nil, client, "default")
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := handler.Invoke(context.Background(), session, json.RawMessage(`{"container_id":"any","command":["echo"],"strict":true}`))
	if domErr == nil {
		t.Fatal("expected error")
	}
	if domErr.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %q", domErr.Code)
	}
}

func TestContainerPSHandler_NilClientReturnsError(t *testing.T) {
	handler := &ContainerPSHandler{k8sOps: &containerK8sAdapter{client: nil}}
	session := domain.Session{
		Runtime: domain.RuntimeRef{Kind: domain.RuntimeKindKubernetes},
	}
	_, domErr := handler.Invoke(context.Background(), session, json.RawMessage(`{"strict":true}`))
	if domErr == nil {
		t.Fatal("expected error for nil k8s client")
	}
}

func TestInvokeK8sLogs_GenericFetchError(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
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
