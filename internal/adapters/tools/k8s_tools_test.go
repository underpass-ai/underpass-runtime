package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestK8sGetPodsHandler_ListPods(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "b-pod",
				Namespace:         "sandbox",
				CreationTimestamp: metav1.NewTime(now),
				Labels:            map[string]string{"app": "beta"},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-b",
				Containers: []corev1.Container{
					{Name: "app", Image: "ghcr.io/acme/beta:1"},
				},
			},
			Status: corev1.PodStatus{
				Phase:    corev1.PodRunning,
				PodIP:    "10.0.0.2",
				HostIP:   "192.168.1.2",
				QOSClass: corev1.PodQOSBestEffort,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app", Ready: true, RestartCount: 1},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "a-pod",
				Namespace:         "sandbox",
				CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
				Labels:            map[string]string{"app": "alpha"},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-a",
				Containers: []corev1.Container{
					{Name: "api", Image: "ghcr.io/acme/alpha:1"},
					{Name: "sidecar", Image: "ghcr.io/acme/sidecar:1"},
				},
			},
			Status: corev1.PodStatus{
				Phase:    corev1.PodRunning,
				PodIP:    "10.0.0.1",
				HostIP:   "192.168.1.1",
				QOSClass: corev1.PodQOSBurstable,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "api", Ready: true, RestartCount: 0},
					{Name: "sidecar", Ready: false, RestartCount: 2, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
				},
			},
		},
	)
	handler := NewK8sGetPodsHandler(client, "default")
	session := domain.Session{
		WorkspacePath: "/workspace/repo",
		AllowedPaths:  []string{"."},
		Principal:     domain.Principal{TenantID: "t", ActorID: "a", Roles: []string{"devops"}},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox","include_containers":true,"include_labels":true,"max_pods":10}`))
	if err != nil {
		t.Fatalf("unexpected k8s.get_pods error: %#v", err)
	}

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", result.Output)
	}
	if output["namespace"] != "sandbox" {
		t.Fatalf("unexpected namespace: %#v", output["namespace"])
	}
	if output["count"] != 2 {
		t.Fatalf("expected count=2, got %#v", output["count"])
	}
	if output["truncated"] != false {
		t.Fatalf("expected truncated=false, got %#v", output["truncated"])
	}
	pods, ok := output["pods"].([]map[string]any)
	if !ok {
		t.Fatalf("expected pods []map, got %T", output["pods"])
	}
	if len(pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods))
	}
	if pods[0]["name"] != "a-pod" || pods[1]["name"] != "b-pod" {
		t.Fatalf("expected deterministic ordering by name, got %#v", pods)
	}
	if len(result.Artifacts) == 0 || result.Artifacts[0].Name != "k8s-get-pods-report.json" {
		t.Fatalf("expected k8s report artifact, got %#v", result.Artifacts)
	}
}

func TestK8sGetPodsHandler_Truncates(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "c-pod", Namespace: "sandbox"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a-pod", Namespace: "sandbox"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "b-pod", Namespace: "sandbox"}},
	)
	handler := NewK8sGetPodsHandler(client, "default")
	session := domain.Session{
		Principal: domain.Principal{TenantID: "t", ActorID: "a", Roles: []string{"devops"}},
	}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox","max_pods":1}`))
	if err != nil {
		t.Fatalf("unexpected k8s.get_pods truncation error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 1 {
		t.Fatalf("expected count=1, got %#v", output["count"])
	}
	if output["truncated"] != true {
		t.Fatalf("expected truncated=true, got %#v", output["truncated"])
	}
	pods := output["pods"].([]map[string]any)
	if pods[0]["name"] != "a-pod" {
		t.Fatalf("expected first pod a-pod after sort, got %#v", pods[0]["name"])
	}
}

func TestK8sGetPodsHandler_WithoutClientFails(t *testing.T) {
	handler := NewK8sGetPodsHandler(nil, "default")
	session := domain.Session{
		Principal: domain.Principal{TenantID: "t", ActorID: "a", Roles: []string{"devops"}},
	}
	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when kubernetes client is missing")
	}
	if err.Code != "execution_failed" {
		t.Fatalf("expected execution_failed, got %s", err.Code)
	}
}

func TestK8sGetServicesHandler_ListAndTruncate(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "b-svc", Namespace: "sandbox"},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeClusterIP,
				Selector: map[string]string{"app": "beta"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}},
			},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "a-svc", Namespace: "sandbox"},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeClusterIP,
				Selector: map[string]string{"app": "alpha"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}},
			},
		},
	)
	handler := NewK8sGetServicesHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox","max_services":1}`))
	if err != nil {
		t.Fatalf("unexpected k8s.get_services error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 1 || output["truncated"] != true {
		t.Fatalf("expected count=1 truncated=true, got %#v", output)
	}
	services := output["services"].([]map[string]any)
	if services[0]["name"] != "a-svc" {
		t.Fatalf("expected deterministic first service a-svc, got %#v", services[0]["name"])
	}
}

func TestK8sGetDeploymentsHandler_ListDeployments(t *testing.T) {
	replicas := int32(3)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "sandbox"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "api", Image: "ghcr.io/acme/api:1"},
						},
					},
				},
			},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas:       2,
				UpdatedReplicas:     2,
				AvailableReplicas:   2,
				UnavailableReplicas: 1,
			},
		},
	)
	handler := NewK8sGetDeploymentsHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox","include_containers":true}`))
	if err != nil {
		t.Fatalf("unexpected k8s.get_deployments error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 1 {
		t.Fatalf("expected count=1, got %#v", output["count"])
	}
	deployments := output["deployments"].([]map[string]any)
	if deployments[0]["name"] != "api" {
		t.Fatalf("expected deployment api, got %#v", deployments[0]["name"])
	}
	containers := deployments[0]["containers"].([]map[string]any)
	if len(containers) != 1 || containers[0]["image"] != "ghcr.io/acme/api:1" {
		t.Fatalf("unexpected deployment containers: %#v", containers)
	}
}

func TestK8sGetImagesHandler_Aggregates(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "sandbox"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "api", Image: "ghcr.io/acme/api:1"},
					{Name: "sidecar", Image: "ghcr.io/acme/sidecar:1"},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-b", Namespace: "sandbox"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "api", Image: "ghcr.io/acme/api:1"},
				},
			},
		},
	)
	handler := NewK8sGetImagesHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox","include_workloads":true}`))
	if err != nil {
		t.Fatalf("unexpected k8s.get_images error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["count"] != 2 {
		t.Fatalf("expected 2 images, got %#v", output["count"])
	}
	images := output["images"].([]map[string]any)
	if images[0]["image"] != "ghcr.io/acme/api:1" {
		t.Fatalf("expected api image first by occurrences, got %#v", images[0]["image"])
	}
	if images[0]["occurrences"] != 2 {
		t.Fatalf("expected api occurrences=2, got %#v", images[0]["occurrences"])
	}
}

func TestK8sGetLogsHandler_RequiresPodName(t *testing.T) {
	handler := NewK8sGetLogsHandler(k8sfake.NewSimpleClientset(), "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox"}`))
	if err == nil {
		t.Fatal("expected invalid_argument when pod_name is missing")
	}
	if err.Code != "invalid_argument" {
		t.Fatalf("expected invalid_argument, got %s", err.Code)
	}
}
