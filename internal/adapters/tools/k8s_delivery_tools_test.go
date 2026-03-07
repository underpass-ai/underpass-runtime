package tools

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestK8sApplyManifestHandler_ConfigMapCreateAndUpdate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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

	configMap, getErr := client.CoreV1().ConfigMaps("sandbox").Get(context.Background(), "delivery-config", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected applied configmap, got error: %v", getErr)
	}
	if configMap.Data["mode"] != "update" {
		t.Fatalf("expected configmap data update, got %#v", configMap.Data)
	}
}

func TestK8sApplyManifestHandler_DeniesUnsupportedKind(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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
	handler := NewK8sApplyManifestHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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
				Namespace:  "sandbox",
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
	handler := NewK8sRolloutStatusHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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
				Namespace:  "sandbox",
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
	handler := NewK8sRolloutStatusHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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

func TestK8sRestartDeploymentHandler_Succeeds(t *testing.T) {
	replicas := int32(0)
	client := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api",
				Namespace: "sandbox",
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
	handler := NewK8sRestartDeploymentHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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

	deployment, getErr := client.AppsV1().Deployments("sandbox").Get(context.Background(), "api", metav1.GetOptions{})
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
	if NewK8sApplyManifestHandler(client, "default").Name() != "k8s.apply_manifest" {
		t.Fatal("unexpected K8sApplyManifestHandler name")
	}
	if NewK8sRolloutStatusHandler(client, "default").Name() != "k8s.rollout_status" {
		t.Fatal("unexpected K8sRolloutStatusHandler name")
	}
	if NewK8sRestartDeploymentHandler(client, "default").Name() != "k8s.restart_deployment" {
		t.Fatal("unexpected K8sRestartDeploymentHandler name")
	}
}

func TestK8sApplyManifestHandler_DeploymentCreateAndUpdate(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	handler := NewK8sApplyManifestHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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
	handler := NewK8sApplyManifestHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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
			ClusterIP:           "10.0.0.1",
			ClusterIPs:          []string{"10.0.0.1"},
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

	if service.Spec.ClusterIP != "10.0.0.1" {
		t.Fatalf("expected ClusterIP preserved, got %q", service.Spec.ClusterIP)
	}
	if len(service.Spec.ClusterIPs) == 0 || service.Spec.ClusterIPs[0] != "10.0.0.1" {
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
	handler := NewK8sApplyManifestHandler(client, "default")
	session := domain.Session{Principal: domain.Principal{Roles: []string{"devops"}}}

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
