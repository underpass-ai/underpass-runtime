//go:build k8s

package tools

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestK8sScaleDeploymentHandler_ErrorPathsAndNoop(t *testing.T) {
	replicas := int32(3)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	client := k8sfake.NewSimpleClientset(deployment)
	handler := NewK8sScaleDeploymentHandler(client, " sandbox-default ")
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	t.Run("missing_deployment_name", func(t *testing.T) {
		_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace": testK8sNamespaceSandbox,
			"replicas":  3,
		}))
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected invalid deployment_name error, got %#v", err)
		}
	})

	t.Run("requires_exactly_one_replicas_field", func(t *testing.T) {
		_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace":       testK8sNamespaceSandbox,
			"deployment_name": "payments-api",
			"replicas":        3,
			"replicas_delta":  1,
		}))
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected replicas field validation error, got %#v", err)
		}
	})

	t.Run("negative_target_denied", func(t *testing.T) {
		_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace":       testK8sNamespaceSandbox,
			"deployment_name": "payments-api",
			"replicas_delta":  -10,
		}))
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected negative target error, got %#v", err)
		}
	})

	t.Run("noop_when_target_unchanged", func(t *testing.T) {
		result, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace":       testK8sNamespaceSandbox,
			"deployment_name": "payments-api",
			"replicas":        3,
		}))
		if err != nil {
			t.Fatalf("unexpected noop scale error: %#v", err)
		}
		output := result.Output.(map[string]any)
		if output["applied"] != false {
			t.Fatalf("expected noop scale result, got %#v", output)
		}
	})

	t.Run("deployment_not_found", func(t *testing.T) {
		_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace":       testK8sNamespaceSandbox,
			"deployment_name": "missing",
			"replicas":        1,
		}))
		if err == nil || err.Code != app.ErrorCodeNotFound {
			t.Fatalf("expected deployment not found, got %#v", err)
		}
	})
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

func TestK8sRestartPodsHandler_RolloutRestartAndInvalidMode(t *testing.T) {
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
	client := k8sfake.NewSimpleClientset(deployment)
	handler := NewK8sRestartPodsHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "payments-api",
		"mode":            "rollout_restart",
	}))
	if err != nil {
		t.Fatalf("unexpected rollout restart error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["mode"] != "rollout_restart" || output["pods_affected"] != 2 {
		t.Fatalf("unexpected rollout restart output: %#v", output)
	}

	updated, getErr := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "payments-api", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("get deployment: %v", getErr)
	}
	if updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Fatalf("expected restart annotation, got %#v", updated.Spec.Template.Annotations)
	}

	_, err = handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "payments-api",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected missing mode error, got %#v", err)
	}

	_, err = handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "payments-api",
		"mode":            "unknown_mode",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid mode error, got %#v", err)
	}
}

func TestK8sCircuitBreakHandler_CreatesNetworkPolicy(t *testing.T) {
	targetService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
		Spec: corev1.ServiceSpec{
			Selector:  map[string]string{"app": "payments-api"},
			ClusterIP: "10.0.0.10",
		},
	}
	downstreamService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "provider-gateway", Namespace: testK8sNamespaceSandbox},
		Spec: corev1.ServiceSpec{
			Selector:  map[string]string{"app": "provider-gateway"},
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

func TestK8sCircuitBreakHandler_UpdateAndErrorPaths(t *testing.T) {
	targetService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
		Spec: corev1.ServiceSpec{
			Selector:  map[string]string{"app": "payments-api"},
			ClusterIP: "10.0.0.10",
		},
	}
	downstreamService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "provider-gateway", Namespace: testK8sNamespaceSandbox},
		Spec: corev1.ServiceSpec{
			Selector:  map[string]string{"app": "provider-gateway"},
			ClusterIP: "10.0.0.20",
		},
	}
	existingPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:            circuitBreakPolicyID(testK8sNamespaceSandbox, "payments-api", "provider-gateway"),
			Namespace:       testK8sNamespaceSandbox,
			ResourceVersion: "1",
		},
	}
	client := k8sfake.NewSimpleClientset(targetService, downstreamService, existingPolicy)
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
		t.Fatalf("unexpected update circuit_break error: %#v", err)
	}
	output := result.Output.(map[string]any)
	policyID := output["policy_id"].(string)

	policy, getErr := client.NetworkingV1().NetworkPolicies(testK8sNamespaceSandbox).Get(context.Background(), policyID, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected updated network policy, got error: %v", getErr)
	}
	if policy.Annotations["underpass.ai/downstream"] != "provider-gateway" {
		t.Fatalf("unexpected updated policy annotations: %#v", policy.Annotations)
	}

	t.Run("target_without_selector", func(t *testing.T) {
		client := k8sfake.NewSimpleClientset(
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "payments-api", Namespace: testK8sNamespaceSandbox},
				Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.10"},
			},
			downstreamService.DeepCopy(),
		)
		selectorHandler := NewK8sCircuitBreakHandler(client, testK8sNamespaceDefault)
		_, err := selectorHandler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace":      testK8sNamespaceSandbox,
			"target_service": "payments-api",
			"downstream":     "provider-gateway",
			"ttl_seconds":    120,
		}))
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected selector validation error, got %#v", err)
		}
	})

	t.Run("downstream_without_cluster_ip", func(t *testing.T) {
		client := k8sfake.NewSimpleClientset(
			targetService.DeepCopy(),
			&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "provider-gateway", Namespace: testK8sNamespaceSandbox},
				Spec:       corev1.ServiceSpec{ClusterIP: "None"},
			},
		)
		clusterIPHandler := NewK8sCircuitBreakHandler(client, testK8sNamespaceDefault)
		_, err := clusterIPHandler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
			"namespace":      testK8sNamespaceSandbox,
			"target_service": "payments-api",
			"downstream":     "provider-gateway",
			"ttl_seconds":    120,
		}))
		if err == nil || err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected cluster IP validation error, got %#v", err)
		}
	})

	handler.mu.Lock()
	if timer := handler.timers[policyID]; timer != nil {
		timer.Stop()
	}
	handler.mu.Unlock()
}

func TestK8sCircuitBreakHandler_ReconcilesExistingPolicies(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()

	t.Run("deletes_expired_policy_on_startup", func(t *testing.T) {
		expiredPolicy := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      circuitBreakPolicyID(testK8sNamespaceSandbox, "payments-api", "provider-gateway"),
				Namespace: testK8sNamespaceSandbox,
				Annotations: map[string]string{
					circuitBreakExpiresAtAnnotation: now.Add(-time.Minute).Format(time.RFC3339),
				},
			},
		}
		client := k8sfake.NewSimpleClientset(expiredPolicy)
		handler := newK8sCircuitBreakHandler(
			client,
			testK8sNamespaceSandbox,
			func() time.Time { return now },
			func(time.Duration, func()) *time.Timer {
				t.Fatal("did not expect cleanup timer for expired policy")
				return time.NewTimer(time.Hour)
			},
		)

		if _, err := client.NetworkingV1().NetworkPolicies(testK8sNamespaceSandbox).Get(context.Background(), expiredPolicy.Name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("expected expired policy to be deleted, got err=%v", err)
		}

		handler.mu.Lock()
		defer handler.mu.Unlock()
		if len(handler.timers) != 0 {
			t.Fatalf("expected no timers after deleting expired policy, got %#v", handler.timers)
		}
	})

	t.Run("schedules_future_policy_cleanup_on_startup", func(t *testing.T) {
		expiresAt := now.Add(2 * time.Minute)
		policyName := circuitBreakPolicyID(testK8sNamespaceSandbox, "payments-api", "provider-gateway")
		futurePolicy := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      policyName,
				Namespace: testK8sNamespaceSandbox,
				Annotations: map[string]string{
					circuitBreakExpiresAtAnnotation: expiresAt.Format(time.RFC3339),
				},
			},
		}
		client := k8sfake.NewSimpleClientset(futurePolicy)
		var scheduledTTL time.Duration
		var scheduledCleanup func()
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()

		handler := newK8sCircuitBreakHandler(
			client,
			testK8sNamespaceSandbox,
			func() time.Time { return now },
			func(ttl time.Duration, cleanup func()) *time.Timer {
				scheduledTTL = ttl
				scheduledCleanup = cleanup
				return timer
			},
		)

		if scheduledCleanup == nil {
			t.Fatal("expected startup reconcile to schedule cleanup")
		}
		if scheduledTTL != 2*time.Minute {
			t.Fatalf("expected scheduled ttl=2m, got %s", scheduledTTL)
		}
		if _, err := client.NetworkingV1().NetworkPolicies(testK8sNamespaceSandbox).Get(context.Background(), policyName, metav1.GetOptions{}); err != nil {
			t.Fatalf("expected policy to still exist before cleanup callback, got %v", err)
		}

		scheduledCleanup()

		if _, err := client.NetworkingV1().NetworkPolicies(testK8sNamespaceSandbox).Get(context.Background(), policyName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("expected scheduled cleanup to delete policy, got err=%v", err)
		}

		handler.mu.Lock()
		defer handler.mu.Unlock()
		if len(handler.timers) != 0 {
			t.Fatalf("expected timers map to be empty after cleanup callback, got %#v", handler.timers)
		}
	})
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

func TestK8sSaturationHelpers(t *testing.T) {
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "payments-api"}},
		},
	}

	t.Run("build_restart_selector", func(t *testing.T) {
		if got := buildRestartPodsSelector(deployment, "pod-template-hash=abc123"); got != "app=payments-api,pod-template-hash=abc123" {
			t.Fatalf("unexpected combined selector: %q", got)
		}
		if got := buildRestartPodsSelector(deployment, ""); got != "app=payments-api" {
			t.Fatalf("unexpected selector without extra: %q", got)
		}
		if got := buildRestartPodsSelector(&appsv1.Deployment{}, "pod-template-hash=abc123"); got != "<none>,pod-template-hash=abc123" {
			t.Fatalf("unexpected selector without base: %q", got)
		}
	})

	t.Run("network_policy_peer_ipv6", func(t *testing.T) {
		peer := networkPolicyPeerAllowAllExcept("fd00::10")
		if peer.IPBlock == nil || peer.IPBlock.CIDR != "::/0" || len(peer.IPBlock.Except) != 1 || peer.IPBlock.Except[0] != "fd00::10/128" {
			t.Fatalf("unexpected IPv6 IPBlock: %#v", peer.IPBlock)
		}
	})

	t.Run("schedule_policy_cleanup_replaces_timer", func(t *testing.T) {
		handler := NewK8sCircuitBreakHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		if handler.now().IsZero() {
			t.Fatal("expected default clock to be initialized")
		}
		handler.schedulePolicyCleanup(testK8sNamespaceSandbox, "policy-a", time.Hour)
		first := handler.timers["policy-a"]
		handler.schedulePolicyCleanup(testK8sNamespaceSandbox, "policy-a", time.Hour)
		second := handler.timers["policy-a"]
		if first == nil || second == nil || first == second {
			t.Fatalf("expected timer replacement, got first=%v second=%v", first, second)
		}
		second.Stop()
	})
}
