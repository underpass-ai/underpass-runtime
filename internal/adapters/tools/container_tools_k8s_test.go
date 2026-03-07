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
