//go:build k8s

package tools

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestK8sScaleDeploymentHandler_AbsoluteAndDelta(t *testing.T) {
	replicas := int32(3)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	client := k8sfake.NewSimpleClientset(deployment)
	handler := NewK8sScaleDeploymentHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "payments-api",
		"replicas":        5,
	}))
	if err != nil {
		t.Fatalf("unexpected scale error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["previous_replicas"] != 3 || output["target_replicas"] != 5 {
		t.Fatalf("unexpected scale output: %#v", output)
	}

	updated, _ := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "payments-api", metav1.GetOptions{})
	if got := derefInt32(updated.Spec.Replicas); got != 5 {
		t.Fatalf("expected replicas=5, got %d", got)
	}

	_, err = handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "payments-api",
		"replicas_delta":  -2,
	}))
	if err != nil {
		t.Fatalf("unexpected delta scale error: %#v", err)
	}
	updated, _ = client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "payments-api", metav1.GetOptions{})
	if got := derefInt32(updated.Spec.Replicas); got != 3 {
		t.Fatalf("expected replicas=3 after delta, got %d", got)
	}
}

func TestK8sRestartPodsHandler_LabelSelectorMode(t *testing.T) {
	replicas := int32(2)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "payments-api"}},
			},
		},
	}
	podA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api-a",
			Namespace: testK8sNamespaceSandbox,
			Labels:    map[string]string{"app": "payments-api", "pod-template-hash": "abc123"},
		},
	}
	podB := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payments-api-b",
			Namespace: testK8sNamespaceSandbox,
			Labels:    map[string]string{"app": "payments-api", "pod-template-hash": "abc123"},
		},
	}
	client := k8sfake.NewSimpleClientset(deployment, podA, podB)
	handler := NewK8sRestartPodsHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "payments-api",
		"mode":            "label_selector",
		"label_selector":  "pod-template-hash=abc123",
		"max_pods":        1,
	}))
	if err != nil {
		t.Fatalf("unexpected restart_pods error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["pods_affected"] != 1 {
		t.Fatalf("expected 1 affected pod, got %#v", output)
	}

	pods, listErr := client.CoreV1().Pods(testK8sNamespaceSandbox).List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("list pods: %v", listErr)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected one pod remaining, got %d", len(pods.Items))
	}
}

func TestK8sCircuitBreakHandler_CreatesNetworkPolicy(t *testing.T) {
	targetService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "payments-api"},
			ClusterIP: "10.0.0.10",
		},
	}
	downstreamService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "provider-gateway", Namespace: testK8sNamespaceSandbox},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "provider-gateway"},
			ClusterIP: "10.0.0.20",
		},
	}
	client := k8sfake.NewSimpleClientset(targetService, downstreamService)
	handler := NewK8sCircuitBreakHandler(client, testK8sNamespaceDefault)
	handler.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	session := domain.Session{Principal: domain.Principal{Roles: []string{"platform_admin"}}}

	result, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":      testK8sNamespaceSandbox,
		"target_service": "payments-api",
		"downstream":     "provider-gateway",
		"ttl_seconds":    120,
	}))
	if err != nil {
		t.Fatalf("unexpected circuit_break error: %#v", err)
	}
	output := result.Output.(map[string]any)
	policyID := output["policy_id"].(string)

	policy, getErr := client.NetworkingV1().NetworkPolicies(testK8sNamespaceSandbox).Get(context.Background(), policyID, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected network policy, got error: %v", getErr)
	}
	if policy.Spec.PodSelector.MatchLabels["app"] != "payments-api" {
		t.Fatalf("unexpected pod selector: %#v", policy.Spec.PodSelector.MatchLabels)
	}
	if len(policy.Spec.Egress) != 1 || len(policy.Spec.Egress[0].To) != 1 {
		t.Fatalf("unexpected egress rules: %#v", policy.Spec.Egress)
	}
	ipBlock := policy.Spec.Egress[0].To[0].IPBlock
	if ipBlock == nil || len(ipBlock.Except) != 1 || ipBlock.Except[0] != "10.0.0.20/32" {
		t.Fatalf("unexpected IPBlock: %#v", ipBlock)
	}

	handler.mu.Lock()
	if timer := handler.timers[policyID]; timer != nil {
		timer.Stop()
	}
	handler.mu.Unlock()
}

func TestK8sSaturationHandlerNames(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	if NewK8sScaleDeploymentHandler(client, testK8sNamespaceDefault).Name() != "k8s.scale_deployment" {
		t.Fatal("expected k8s.scale_deployment")
	}
	if NewK8sRestartPodsHandler(client, testK8sNamespaceDefault).Name() != "k8s.restart_pods" {
		t.Fatal("expected k8s.restart_pods")
	}
	if NewK8sCircuitBreakHandler(client, testK8sNamespaceDefault).Name() != "k8s.circuit_break" {
		t.Fatal("expected k8s.circuit_break")
	}
}

func TestK8sCircuitBreakHandler_TTLDenied(t *testing.T) {
	handler := NewK8sCircuitBreakHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{"platform_admin"}}}
	_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":      testK8sNamespaceSandbox,
		"target_service": "payments-api",
		"downstream":     "provider-gateway",
		"ttl_seconds":    30,
	}))
	if err == nil || err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected ttl denial, got %#v", err)
	}
}
