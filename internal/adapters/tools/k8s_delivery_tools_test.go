//go:build k8s

package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	testK8sNamespaceSandbox = "sandbox"
	testK8sNamespaceDefault = "default"
	testK8sRoleDevops       = "devops"
	testK8sClusterIP        = "10.0.0.1" //NOSONAR — fake K8s cluster IP for tests
)

func TestK8sApplyManifestHandler_ConfigMapCreateAndUpdate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	createManifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: delivery-config
data:
  mode: create
`
	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(createManifest)+`}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.apply_manifest create error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["applied_count"] != 1 || output["created_count"] != 1 {
		t.Fatalf("unexpected apply counts for create: %#v", output)
	}
	resources := output["resources"].([]map[string]any)
	if len(resources) != 1 || resources[0]["operation"] != "created" {
		t.Fatalf("unexpected apply resources for create: %#v", resources)
	}

	updateManifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: delivery-config
data:
  mode: update
`
	result, err = handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(updateManifest)+`}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.apply_manifest update error: %#v", err)
	}
	output = result.Output.(map[string]any)
	if output["updated_count"] != 1 {
		t.Fatalf("unexpected apply counts for update: %#v", output)
	}
	resources = output["resources"].([]map[string]any)
	if len(resources) != 1 || resources[0]["operation"] != "updated" {
		t.Fatalf("unexpected apply resources for update: %#v", resources)
	}

	configMap, getErr := client.CoreV1().ConfigMaps(testK8sNamespaceSandbox).Get(context.Background(), "delivery-config", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected applied configmap, got error: %v", getErr)
	}
	if configMap.Data["mode"] != "update" {
		t.Fatalf("expected configmap data update, got %#v", configMap.Data)
	}
}

func TestK8sApplyManifestHandler_DeniesUnsupportedKind(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	manifest := `apiVersion: v1
kind: Secret
metadata:
  name: not-allowed
type: Opaque
stringData:
  key: value
`
	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(manifest)+`}`),
	)
	if err == nil {
		t.Fatal("expected policy denial for unsupported kind")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy_denied, got %s", err.Code)
	}
}

func TestK8sApplyManifestHandler_DeniesNamespaceMismatch(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: wrong-namespace
  namespace: other
data:
  key: value
`
	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(manifest)+`}`),
	)
	if err == nil {
		t.Fatal("expected policy denial for namespace mismatch")
	}
	if err.Code != app.ErrorCodePolicyDenied {
		t.Fatalf("expected policy_denied, got %s", err.Code)
	}
}

func TestK8sRolloutStatusHandler_Succeeds(t *testing.T) {
	replicas := int32(1)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "api",
				Namespace:  testK8sNamespaceSandbox,
				Generation: 3,
			},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 3,
				UpdatedReplicas:    1,
				ReadyReplicas:      1,
				AvailableReplicas:  1,
			},
		},
	)
	handler := NewK8sRolloutStatusHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","timeout_seconds":2,"poll_interval_ms":100}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.rollout_status error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "completed" {
		t.Fatalf("expected completed status, got %#v", output["status"])
	}
	rollout := output["rollout"].(map[string]any)
	if rollout["completed"] != true {
		t.Fatalf("expected rollout completed=true, got %#v", rollout)
	}
}

func TestK8sRolloutStatusHandler_Timeout(t *testing.T) {
	replicas := int32(1)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "api",
				Namespace:  testK8sNamespaceSandbox,
				Generation: 2,
			},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration:  1,
				UpdatedReplicas:     0,
				ReadyReplicas:       0,
				AvailableReplicas:   0,
				UnavailableReplicas: 1,
			},
		},
	)
	handler := NewK8sRolloutStatusHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","timeout_seconds":1,"poll_interval_ms":100}`),
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err.Code != app.ErrorCodeTimeout {
		t.Fatalf("expected timeout code, got %s", err.Code)
	}
}

func TestK8sRolloutPauseHandler_Succeeds(t *testing.T) {
	replicas := int32(1)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testK8sNamespaceSandbox},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "busybox:1.36"}}},
				},
			},
		},
	)
	handler := NewK8sRolloutPauseHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.rollout_pause error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["paused"] != true {
		t.Fatalf("expected paused=true, got %#v", output["paused"])
	}

	deployment, getErr := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "api", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected deployment after pause, got %v", getErr)
	}
	if !deployment.Spec.Paused {
		t.Fatal("expected deployment to be paused")
	}
}

func TestK8sRolloutUndoHandler_Succeeds(t *testing.T) {
	replicas := int32(2)
	controller := true
	deploymentUID := types.UID("deploy-uid")
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testK8sNamespaceSandbox, UID: deploymentUID},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "new"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-new",
				Namespace:         testK8sNamespaceSandbox,
				UID:               types.UID("rs-new"),
				CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "new"},
				Annotations:       map[string]string{"deployment.kubernetes.io/revision": "5"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "new"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 2, AvailableReplicas: 2},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-old",
				Namespace:         testK8sNamespaceSandbox,
				UID:               types.UID("rs-old"),
				CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "old"},
				Annotations:       map[string]string{"deployment.kubernetes.io/revision": "4"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "old"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:1"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 2, AvailableReplicas: 2},
		},
	)
	handler := NewK8sRolloutUndoHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.rollout_undo error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["rolled_back"] != true || output["target_revision"] != 4 {
		t.Fatalf("unexpected rollout_undo output: %#v", output)
	}

	deployment, getErr := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "api", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected deployment after rollback, got %v", getErr)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "ghcr.io/acme/api:1" {
		t.Fatalf("expected rollback to previous template, got %#v", deployment.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestK8sRolloutUndoHandler_AllowsScaledDownPreviousReplicaSet(t *testing.T) {
	replicas := int32(1)
	zeroReplicas := int32(0)
	controller := true
	deploymentUID := types.UID("deploy-uid")
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testK8sNamespaceSandbox, UID: deploymentUID},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "new"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-new",
				Namespace:         testK8sNamespaceSandbox,
				UID:               types.UID("rs-new"),
				CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "new"},
				Annotations:       map[string]string{"deployment.kubernetes.io/revision": "5"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "new"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-old",
				Namespace:         testK8sNamespaceSandbox,
				UID:               types.UID("rs-old"),
				CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "old"},
				Annotations: map[string]string{
					"deployment.kubernetes.io/revision":         "4",
					"deployment.kubernetes.io/desired-replicas": "1",
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &zeroReplicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "old"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:1"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{},
		},
	)
	handler := NewK8sRolloutUndoHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.rollout_undo error for scaled-down previous rs: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["rolled_back"] != true || output["target_revision"] != 4 {
		t.Fatalf("unexpected rollout_undo output for scaled-down previous rs: %#v", output)
	}
}

func TestK8sRolloutUndoHandler_DeniesYoungRollout(t *testing.T) {
	replicas := int32(1)
	controller := true
	deploymentUID := types.UID("deploy-uid")
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testK8sNamespaceSandbox, UID: deploymentUID},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "new"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-new",
				Namespace:         testK8sNamespaceSandbox,
				CreationTimestamp: metav1.NewTime(now.Add(-30 * time.Second)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "new"},
				Annotations:       map[string]string{"deployment.kubernetes.io/revision": "5"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "new"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-old",
				Namespace:         testK8sNamespaceSandbox,
				CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "old"},
				Annotations:       map[string]string{"deployment.kubernetes.io/revision": "4"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "old"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:1"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1},
		},
	)
	handler := NewK8sRolloutUndoHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`))
	if err == nil {
		t.Fatal("expected rollout_too_young denial")
	}
	if err.Code != app.ErrorCodeRolloutTooYoung {
		t.Fatalf("expected rollout_too_young, got %#v", err)
	}
}

func TestK8sGetReplicaSetsHandler_ValidationAndLookupFailures(t *testing.T) {
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	t.Run("invalid_args", func(t *testing.T) {
		handler := NewK8sGetReplicaSetsHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{`))
		if err == nil {
			t.Fatal("expected invalid args error")
		}
		if err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("expected invalid_argument, got %#v", err)
		}
	})

	t.Run("missing_client", func(t *testing.T) {
		handler := NewK8sGetReplicaSetsHandler(nil, testK8sNamespaceDefault)
		_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox"}`))
		if err == nil {
			t.Fatal("expected execution_failed when client is nil")
		}
		if err.Code != app.ErrorCodeExecutionFailed {
			t.Fatalf("expected execution_failed, got %#v", err)
		}
	})

	t.Run("deployment_not_found", func(t *testing.T) {
		handler := NewK8sGetReplicaSetsHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(
			context.Background(),
			session,
			json.RawMessage(`{"namespace":"sandbox","deployment_name":"missing"}`),
		)
		if err == nil {
			t.Fatal("expected not_found error")
		}
		if err.Code != app.ErrorCodeNotFound {
			t.Fatalf("expected not_found, got %#v", err)
		}
	})
}

func TestK8sGetReplicaSetsHandler_ListsNamespaceReplicaSetsWithoutDeploymentFilter(t *testing.T) {
	replicas := int32(1)
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-new",
				Namespace:         testK8sNamespaceSandbox,
				CreationTimestamp: metav1.NewTime(now),
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "5"},
			},
			Spec:   appsv1.ReplicaSetSpec{Replicas: &replicas},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-old",
				Namespace:         testK8sNamespaceSandbox,
				CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "4"},
			},
			Spec:   appsv1.ReplicaSetSpec{Replicas: &replicas},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
	)
	handler := NewK8sGetReplicaSetsHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox"}`))
	if err != nil {
		t.Fatalf("unexpected k8s.get_replicasets list error: %#v", err)
	}

	output := result.Output.(map[string]any)
	replicaSets := output["replicasets"].([]map[string]any)
	if len(replicaSets) != 2 {
		t.Fatalf("expected 2 replicasets, got %#v", replicaSets)
	}
	if output["summary"] != "listed 2 replicasets in namespace sandbox" {
		t.Fatalf("unexpected summary: %#v", output["summary"])
	}
}

func TestTargetRollbackReplicaSet_ErrorPaths(t *testing.T) {
	replicas := int32(1)
	current := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-rs-current"}}
	unhealthy := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "api-rs-old",
			Annotations: map[string]string{k8sDeploymentRevisionAnnotation: "4"},
		},
		Spec:   appsv1.ReplicaSetSpec{Replicas: &replicas},
		Status: appsv1.ReplicaSetStatus{},
	}

	t.Run("empty", func(t *testing.T) {
		_, err := targetRollbackReplicaSet(nil, nil, 0)
		if err == nil {
			t.Fatal("expected no previous healthy replicaset error")
		}
		if err.Code != app.ErrorCodeNoPreviousHealthyReplicaSet {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("revision_not_found", func(t *testing.T) {
		_, err := targetRollbackReplicaSet([]appsv1.ReplicaSet{unhealthy}, nil, 9)
		if err == nil {
			t.Fatal("expected revision not found error")
		}
		if err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("requested_revision_unhealthy", func(t *testing.T) {
		_, err := targetRollbackReplicaSet([]appsv1.ReplicaSet{unhealthy}, nil, 4)
		if err == nil {
			t.Fatal("expected unhealthy rollback target error")
		}
		if err.Code != app.ErrorCodeNoPreviousHealthyReplicaSet {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("no_previous_healthy", func(t *testing.T) {
		_, err := targetRollbackReplicaSet([]appsv1.ReplicaSet{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "api-rs-current",
					Annotations: map[string]string{k8sDeploymentRevisionAnnotation: "5"},
				},
				Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
			},
			unhealthy,
		}, current, 0)
		if err == nil {
			t.Fatal("expected no previous healthy replicaset error")
		}
		if err.Code != app.ErrorCodeNoPreviousHealthyReplicaSet {
			t.Fatalf("unexpected error: %#v", err)
		}
	})
}

func TestReplicaSetRollbackHelpers(t *testing.T) {
	if replicaSetHealthyRollbackTarget(nil) {
		t.Fatal("nil replicaset should not be a healthy rollback target")
	}
	if replicaSetDesiredReplicas(nil) != 0 {
		t.Fatal("nil replicaset should report zero desired replicas")
	}
	if replicaSetDesiredReplicas(&appsv1.ReplicaSet{}) != 0 {
		t.Fatal("missing annotation should report zero desired replicas")
	}
	if replicaSetDesiredReplicas(&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{k8sDesiredReplicasAnnotation: "oops"}},
	}) != 0 {
		t.Fatal("invalid desired replicas annotation should report zero")
	}
	if !replicaSetHealthyRollbackTarget(&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{k8sDesiredReplicasAnnotation: "2"}},
	}) {
		t.Fatal("desired replicas annotation should make scaled-down replicaset a valid rollback target")
	}
}

func TestReplicaSetSelectionHelpers(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", UID: types.UID("deploy-uid")},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "hash-a"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
			},
		},
	}

	byHash := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "api-rs-hash",
			Labels:          map[string]string{"pod-template-hash": "hash-a"},
			Annotations:     map[string]string{k8sDeploymentRevisionAnnotation: "5"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api", UID: types.UID("deploy-uid")}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: deployment.Spec.Template,
		},
	}
	byTemplate := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "api-rs-template",
			Annotations:     map[string]string{k8sDeploymentRevisionAnnotation: "4"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api"}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:1"}}},
			},
		},
	}
	wrongOwner := appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "api-rs-other",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "other", UID: types.UID("other")}},
		},
	}

	if !replicaSetOwnedByDeployment(byHash, deployment) {
		t.Fatal("expected matching owner reference to be accepted")
	}
	if !replicaSetOwnedByDeployment(byTemplate, deployment) {
		t.Fatal("expected owner reference without UID to be accepted for matching deployment name")
	}
	if replicaSetOwnedByDeployment(wrongOwner, deployment) {
		t.Fatal("expected mismatched owner reference to be rejected")
	}
	if !replicaSetOwnedByDeployment(wrongOwner, nil) {
		t.Fatal("expected nil deployment to accept replicaset")
	}

	if got := replicaSetRevision(nil); got != 0 {
		t.Fatalf("expected nil revision to be zero, got %d", got)
	}
	if got := replicaSetRevision(&appsv1.ReplicaSet{}); got != 0 {
		t.Fatalf("expected missing revision to be zero, got %d", got)
	}
	if got := replicaSetRevision(&appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{k8sDeploymentRevisionAnnotation: "oops"}},
	}); got != 0 {
		t.Fatalf("expected invalid revision to be zero, got %d", got)
	}

	if got := replicaSetTemplateHash(nil); got != "" {
		t.Fatalf("expected nil template hash to be empty, got %q", got)
	}
	if got := replicaSetTemplateHash(&byHash); got != "hash-a" {
		t.Fatalf("expected label template hash, got %q", got)
	}
	if got := replicaSetTemplateHash(&appsv1.ReplicaSet{
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"pod-template-hash": "hash-b"}}},
		},
	}); got != "hash-b" {
		t.Fatalf("expected template label hash fallback, got %q", got)
	}

	if got := currentReplicaSet(deployment, []appsv1.ReplicaSet{byTemplate, byHash}); got == nil || got.Name != "api-rs-hash" {
		t.Fatalf("expected currentReplicaSet to prefer pod-template-hash, got %#v", got)
	}

	templateDeployment := deployment.DeepCopy()
	templateDeployment.Spec.Template = byTemplate.Spec.Template
	if got := currentReplicaSet(templateDeployment, []appsv1.ReplicaSet{wrongOwner, byTemplate}); got == nil || got.Name != "api-rs-template" {
		t.Fatalf("expected currentReplicaSet to fall back to template match, got %#v", got)
	}

	sortNow := time.Now().UTC()
	sorted := []appsv1.ReplicaSet{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "b",
				CreationTimestamp: metav1.NewTime(sortNow),
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "5"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "a",
				CreationTimestamp: metav1.NewTime(sortNow),
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "5"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "old",
				CreationTimestamp: metav1.NewTime(sortNow.Add(-time.Minute)),
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "4"},
			},
		},
	}
	sortReplicaSets(sorted)
	if sorted[0].Name != "a" || sorted[1].Name != "b" || sorted[2].Name != "old" {
		t.Fatalf("unexpected sort order: %#v", sorted)
	}
}

func TestK8sRolloutResultSummaries(t *testing.T) {
	pauseOutput := k8sRolloutPauseResult("sandbox", "api", 7, false).Output.(map[string]any)
	if pauseOutput["summary"] != "deployment sandbox/api is already paused" {
		t.Fatalf("unexpected pause summary: %#v", pauseOutput)
	}

	undoOutput := k8sRolloutUndoResult("sandbox", "api", 5, 5, false, 7).Output.(map[string]any)
	if undoOutput["summary"] != "deployment sandbox/api is already at revision 5" {
		t.Fatalf("unexpected undo summary: %#v", undoOutput)
	}
}

func TestK8sRolloutPauseHandler_AlreadyPaused(t *testing.T) {
	replicas := int32(1)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testK8sNamespaceSandbox},
			Spec: appsv1.DeploymentSpec{
				Paused:   true,
				Replicas: &replicas,
			},
			Status: appsv1.DeploymentStatus{ObservedGeneration: 4},
		},
	)
	handler := NewK8sRolloutPauseHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api"}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.rollout_pause error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["summary"] != "deployment sandbox/api is already paused" {
		t.Fatalf("unexpected output for already paused deployment: %#v", output)
	}
}

func TestK8sRolloutPauseHandler_ValidationAndLookupFailures(t *testing.T) {
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	t.Run("missing_deployment_name", func(t *testing.T) {
		handler := NewK8sRolloutPauseHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox"}`))
		if err == nil {
			t.Fatal("expected invalid_argument when deployment_name is missing")
		}
		if err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("deployment_not_found", func(t *testing.T) {
		handler := NewK8sRolloutPauseHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(
			context.Background(),
			session,
			json.RawMessage(`{"namespace":"sandbox","deployment_name":"missing"}`),
		)
		if err == nil {
			t.Fatal("expected not_found when deployment is missing")
		}
		if err.Code != app.ErrorCodeNotFound {
			t.Fatalf("unexpected error: %#v", err)
		}
	})
}

func TestK8sRolloutUndoHandler_AlreadyAtRequestedRevision(t *testing.T) {
	replicas := int32(1)
	controller := true
	deploymentUID := types.UID("deploy-uid")
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: testK8sNamespaceSandbox, UID: deploymentUID},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "current"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
			Status: appsv1.DeploymentStatus{ObservedGeneration: 7},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-current",
				Namespace:         testK8sNamespaceSandbox,
				CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "current"},
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "5"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "current"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:2"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "api-rs-old",
				Namespace:         testK8sNamespaceSandbox,
				CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
				Labels:            map[string]string{"app": "api", "pod-template-hash": "old"},
				Annotations:       map[string]string{k8sDeploymentRevisionAnnotation: "4"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "api",
					UID:        deploymentUID,
					Controller: &controller,
				}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api", "pod-template-hash": "old"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "ghcr.io/acme/api:1"}}},
				},
			},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
	)
	handler := NewK8sRolloutUndoHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","to_revision":5}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.rollout_undo error: %#v", err)
	}

	output := result.Output.(map[string]any)
	if output["rolled_back"] != false || output["target_revision"] != 5 {
		t.Fatalf("unexpected output for already-at-revision rollback: %#v", output)
	}
}

func TestK8sRolloutUndoHandler_ValidationAndLookupFailures(t *testing.T) {
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	t.Run("missing_deployment_name", func(t *testing.T) {
		handler := NewK8sRolloutUndoHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(context.Background(), session, json.RawMessage(`{"namespace":"sandbox"}`))
		if err == nil {
			t.Fatal("expected invalid_argument when deployment_name is missing")
		}
		if err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("negative_revision", func(t *testing.T) {
		handler := NewK8sRolloutUndoHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(
			context.Background(),
			session,
			json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","to_revision":-1}`),
		)
		if err == nil {
			t.Fatal("expected invalid_argument for negative revision")
		}
		if err.Code != app.ErrorCodeInvalidArgument {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("deployment_not_found", func(t *testing.T) {
		handler := NewK8sRolloutUndoHandler(k8sfake.NewSimpleClientset(), testK8sNamespaceDefault)
		_, err := handler.Invoke(
			context.Background(),
			session,
			json.RawMessage(`{"namespace":"sandbox","deployment_name":"missing"}`),
		)
		if err == nil {
			t.Fatal("expected not_found when deployment is missing")
		}
		if err.Code != app.ErrorCodeNotFound {
			t.Fatalf("unexpected error: %#v", err)
		}
	})
}

func TestK8sRestartDeploymentHandler_Succeeds(t *testing.T) {
	replicas := int32(0)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api",
				Namespace: testK8sNamespaceSandbox,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "busybox:1.36"}}},
				},
			},
		},
	)
	handler := NewK8sRestartDeploymentHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","deployment_name":"api","wait_for_rollout":false}`),
	)
	if err != nil {
		t.Fatalf("unexpected k8s.restart_deployment error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["rollout_status"] != "pending" {
		t.Fatalf("expected rollout_status=pending, got %#v", output["rollout_status"])
	}
	restartedAt := output["restarted_at"]
	if restartedAt == "" {
		t.Fatalf("expected restarted_at in output, got %#v", output)
	}

	deployment, getErr := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "api", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected deployment after restart, got error: %v", getErr)
	}
	if deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Fatalf("expected restart annotation to be set: %#v", deployment.Spec.Template.Annotations)
	}
}

func quoteJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestK8sDeliveryHandlerNames(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	if NewK8sGetReplicaSetsHandler(client, testK8sNamespaceDefault).Name() != "k8s.get_replicasets" {
		t.Fatal("unexpected K8sGetReplicaSetsHandler name")
	}
	if NewK8sApplyManifestHandler(client, testK8sNamespaceDefault).Name() != "k8s.apply_manifest" {
		t.Fatal("unexpected K8sApplyManifestHandler name")
	}
	if NewK8sRolloutStatusHandler(client, testK8sNamespaceDefault).Name() != "k8s.rollout_status" {
		t.Fatal("unexpected K8sRolloutStatusHandler name")
	}
	if NewK8sRolloutPauseHandler(client, testK8sNamespaceDefault).Name() != "k8s.rollout_pause" {
		t.Fatal("unexpected K8sRolloutPauseHandler name")
	}
	if NewK8sRolloutUndoHandler(client, testK8sNamespaceDefault).Name() != "k8s.rollout_undo" {
		t.Fatal("unexpected K8sRolloutUndoHandler name")
	}
	if NewK8sRestartDeploymentHandler(client, testK8sNamespaceDefault).Name() != "k8s.restart_deployment" {
		t.Fatal("unexpected K8sRestartDeploymentHandler name")
	}
}

func TestK8sApplyManifestHandler_DeploymentCreateAndUpdate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	deployManifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
        - name: my-app
          image: nginx:1.25
`
	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(deployManifest)+`}`),
	)
	if err != nil {
		t.Fatalf("unexpected deployment create error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["created_count"] != 1 {
		t.Fatalf("expected created_count=1 for deployment, got %#v", output)
	}

	// Apply again to trigger the update path.
	result, err = handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(deployManifest)+`}`),
	)
	if err != nil {
		t.Fatalf("unexpected deployment update error: %#v", err)
	}
	output = result.Output.(map[string]any)
	if output["updated_count"] != 1 {
		t.Fatalf("expected updated_count=1 for deployment, got %#v", output)
	}
}

func TestK8sApplyManifestHandler_ServiceCreateAndUpdate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	svcManifest := `apiVersion: v1
kind: Service
metadata:
  name: my-svc
spec:
  selector:
    app: my-app
  ports:
    - name: http
      protocol: TCP
      port: 80
      targetPort: 8080
`
	result, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(svcManifest)+`}`),
	)
	if err != nil {
		t.Fatalf("unexpected service create error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["created_count"] != 1 {
		t.Fatalf("expected created_count=1 for service, got %#v", output)
	}

	// Apply again to trigger the update path (exercises preserveServiceImmutableFields + servicePortKey).
	result, err = handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":`+quoteJSON(svcManifest)+`}`),
	)
	if err != nil {
		t.Fatalf("unexpected service update error: %#v", err)
	}
	output = result.Output.(map[string]any)
	if output["updated_count"] != 1 {
		t.Fatalf("expected updated_count=1 for service, got %#v", output)
	}
}

func TestPreserveServiceImmutableFields_CopiesFields(t *testing.T) {
	ipFamilyPolicy := corev1.IPFamilyPolicySingleStack
	existing := &corev1.Service{
		Spec: corev1.ServiceSpec{
			ClusterIP:           testK8sClusterIP,
			ClusterIPs:          []string{testK8sClusterIP},
			IPFamilies:          []corev1.IPFamily{corev1.IPv4Protocol},
			IPFamilyPolicy:      &ipFamilyPolicy,
			HealthCheckNodePort: 31500,
			Ports: []corev1.ServicePort{
				{Name: "http", Protocol: corev1.ProtocolTCP, Port: 80, NodePort: 30080},
			},
		},
	}
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Protocol: corev1.ProtocolTCP, Port: 80},
			},
		},
	}

	preserveServiceImmutableFields(service, existing)

	if service.Spec.ClusterIP != testK8sClusterIP {
		t.Fatalf("expected ClusterIP preserved, got %q", service.Spec.ClusterIP)
	}
	if len(service.Spec.ClusterIPs) == 0 || service.Spec.ClusterIPs[0] != testK8sClusterIP {
		t.Fatalf("expected ClusterIPs preserved, got %#v", service.Spec.ClusterIPs)
	}
	if len(service.Spec.IPFamilies) == 0 || service.Spec.IPFamilies[0] != corev1.IPv4Protocol {
		t.Fatalf("expected IPFamilies preserved, got %#v", service.Spec.IPFamilies)
	}
	if service.Spec.IPFamilyPolicy == nil || *service.Spec.IPFamilyPolicy != ipFamilyPolicy {
		t.Fatalf("expected IPFamilyPolicy preserved, got %v", service.Spec.IPFamilyPolicy)
	}
	if service.Spec.HealthCheckNodePort != 31500 {
		t.Fatalf("expected HealthCheckNodePort preserved, got %d", service.Spec.HealthCheckNodePort)
	}
	if service.Spec.Ports[0].NodePort != 30080 {
		t.Fatalf("expected NodePort preserved, got %d", service.Spec.Ports[0].NodePort)
	}

	// nil guard: these must not panic
	preserveServiceImmutableFields(nil, existing)
	preserveServiceImmutableFields(service, nil)
}

func TestK8sApplyManifestHandler_EmptyManifestAndNoObjects(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	// blank manifest → k8sInvalidArgument "manifest is required"
	_, err := handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":"   "}`),
	)
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for blank manifest, got %#v", err)
	}

	// YAML with no objects → k8sInvalidArgument "manifest does not contain Kubernetes objects"
	_, err = handler.Invoke(
		context.Background(),
		session,
		json.RawMessage(`{"namespace":"sandbox","manifest":"---\n"}`),
	)
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty YAML manifest, got %#v", err)
	}
}

func TestK8sSetImageHandler_HappyPath(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: testK8sNamespaceSandbox},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "myapp:v1"},
						{Name: "sidecar", Image: "sidecar:v1"},
					},
				},
			},
		},
	}
	client := k8sfake.NewSimpleClientset(deployment)
	handler := NewK8sSetImageHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	// Update specific container.
	result, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "my-app",
		"container_name":  "app",
		"image":           "myapp:v2",
	}))
	if err != nil {
		t.Fatalf("unexpected set_image error: %#v", err)
	}
	output := result.Output.(map[string]any)
	if output["previous_image"] != "myapp:v1" {
		t.Fatalf("expected previous_image=myapp:v1, got %v", output["previous_image"])
	}
	if output["image"] != "myapp:v2" {
		t.Fatalf("expected image=myapp:v2, got %v", output["image"])
	}

	// Verify the deployment was actually updated.
	updated, _ := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "my-app", metav1.GetOptions{})
	if updated.Spec.Template.Spec.Containers[0].Image != "myapp:v2" {
		t.Fatalf("expected container image updated, got %s", updated.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestK8sSetImageHandler_FirstContainerDefault(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: testK8sNamespaceSandbox},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "myapp:v1"}},
				},
			},
		},
	}
	client := k8sfake.NewSimpleClientset(deployment)
	handler := NewK8sSetImageHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	// No container_name → updates first container.
	_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "my-app",
		"image":           "myapp:v3",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %#v", err)
	}
	updated, _ := client.AppsV1().Deployments(testK8sNamespaceSandbox).Get(context.Background(), "my-app", metav1.GetOptions{})
	if updated.Spec.Template.Spec.Containers[0].Image != "myapp:v3" {
		t.Fatalf("expected first container updated, got %s", updated.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestK8sSetImageHandler_Validation(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sSetImageHandler(client, testK8sNamespaceDefault)
	session := domain.Session{Principal: domain.Principal{Roles: []string{testK8sRoleDevops}}}

	// Missing deployment_name.
	_, err := handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"image": "myapp:v1",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected deployment_name required, got %#v", err)
	}

	// Missing image.
	_, err = handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"deployment_name": "my-app",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected image required, got %#v", err)
	}

	// Nil client.
	nilHandler := NewK8sSetImageHandler(nil, testK8sNamespaceDefault)
	_, err = nilHandler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"deployment_name": "my-app",
		"image":           "myapp:v1",
	}))
	if err == nil || err.Code != app.ErrorCodeExecutionFailed {
		t.Fatalf("expected nil client error, got %#v", err)
	}

	// Deployment not found.
	_, err = handler.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "nonexistent",
		"image":           "myapp:v1",
	}))
	if err == nil || err.Code != app.ErrorCodeNotFound {
		t.Fatalf("expected not found, got %#v", err)
	}

	// Container not found.
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "has-containers", Namespace: testK8sNamespaceSandbox},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "myapp:v1"}},
				},
			},
		},
	}
	clientWithDeploy := k8sfake.NewSimpleClientset(deployment)
	handlerWithDeploy := NewK8sSetImageHandler(clientWithDeploy, testK8sNamespaceDefault)
	_, err = handlerWithDeploy.Invoke(context.Background(), session, mustK8sJSON(t, map[string]any{
		"namespace":       testK8sNamespaceSandbox,
		"deployment_name": "has-containers",
		"container_name":  "nonexistent-container",
		"image":           "myapp:v2",
	}))
	if err == nil || err.Code != app.ErrorCodeInvalidArgument {
		t.Fatalf("expected container not found, got %#v", err)
	}
}

func mustK8sJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return data
}
