//go:build k8s

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type K8sScaleDeploymentHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sRestartPodsHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sCircuitBreakHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
	now              func() time.Time

	mu     sync.Mutex
	timers map[string]*time.Timer
}

func NewK8sScaleDeploymentHandler(client kubernetes.Interface, defaultNamespace string) *K8sScaleDeploymentHandler {
	return &K8sScaleDeploymentHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sRestartPodsHandler(client kubernetes.Interface, defaultNamespace string) *K8sRestartPodsHandler {
	return &K8sRestartPodsHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sCircuitBreakHandler(client kubernetes.Interface, defaultNamespace string) *K8sCircuitBreakHandler {
	return &K8sCircuitBreakHandler{
		client:           client,
		defaultNamespace: strings.TrimSpace(defaultNamespace),
		now:              func() time.Time { return time.Now().UTC() },
		timers:           map[string]*time.Timer{},
	}
}

func (h *K8sScaleDeploymentHandler) Name() string {
	return "k8s.scale_deployment"
}

func (h *K8sRestartPodsHandler) Name() string {
	return "k8s.restart_pods"
}

func (h *K8sCircuitBreakHandler) Name() string {
	return "k8s.circuit_break"
}

func (h *K8sScaleDeploymentHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
		Replicas       *int32 `json:"replicas"`
		ReplicasDelta  *int32 `json:"replicas_delta"`
	}{}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}
	if strings.TrimSpace(request.DeploymentName) == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("deployment_name is required")
	}
	if (request.Replicas == nil) == (request.ReplicasDelta == nil) {
		return app.ToolRunResult{}, k8sInvalidArgument("exactly one of replicas or replicas_delta is required")
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	deployment, err := h.client.AppsV1().Deployments(namespace).Get(ctx, request.DeploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeNotFound, Message: "deployment not found", Retryable: false}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s scale deployment failed: %v", err), true)
	}

	previousReplicas := int(derefInt32(deployment.Spec.Replicas))
	targetReplicas := previousReplicas
	if request.Replicas != nil {
		targetReplicas = int(*request.Replicas)
	}
	if request.ReplicasDelta != nil {
		targetReplicas += int(*request.ReplicasDelta)
	}
	if targetReplicas < 0 {
		return app.ToolRunResult{}, k8sInvalidArgument("target replicas must be >= 0")
	}

	applied := targetReplicas != previousReplicas
	if applied {
		replicas := int32(targetReplicas)
		deployment.Spec.Replicas = &replicas
		deployment, err = h.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s scale deployment failed: %v", err), true)
		}
	}

	summary := fmt.Sprintf("scaled deployment %s/%s from %d to %d replicas", namespace, request.DeploymentName, previousReplicas, targetReplicas)
	output := map[string]any{
		k8sDelivKeyNamespace:  namespace,
		k8sDelivKeyDeployment: request.DeploymentName,
		"previous_replicas":   previousReplicas,
		"target_replicas":     targetReplicas,
		"applied":             applied,
		"observed_generation": deployment.Status.ObservedGeneration,
		k8sDelivKeySummary:    summary,
		k8sDelivKeyOutput:     summary,
		k8sDelivKeyExitCode:   0,
	}
	return k8sResult(output, "k8s-scale-deployment-report.json"), nil
}

func (h *K8sRestartPodsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
		Mode           string `json:"mode"`
		LabelSelector  string `json:"label_selector"`
		MaxPods        int    `json:"max_pods"`
	}{}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}
	if strings.TrimSpace(request.DeploymentName) == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("deployment_name is required")
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	deployment, err := h.client.AppsV1().Deployments(namespace).Get(ctx, request.DeploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeNotFound, Message: "deployment not found", Retryable: false}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s restart pods failed: %v", err), true)
	}

	mode := strings.TrimSpace(request.Mode)
	if mode == "" {
		mode = "rollout_restart"
	}
	output := map[string]any{
		k8sDelivKeyNamespace:  namespace,
		k8sDelivKeyDeployment: request.DeploymentName,
		"mode":                mode,
	}
	switch mode {
	case "rollout_restart":
		if deployment.Spec.Template.Annotations == nil {
			deployment.Spec.Template.Annotations = map[string]string{}
		}
		restartedAt := time.Now().UTC().Format(time.RFC3339)
		deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt
		updated, updateErr := h.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if updateErr != nil {
			return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s restart pods failed: %v", updateErr), true)
		}
		output["pods_affected"] = int(derefInt32(updated.Spec.Replicas))
		output["rollout_revision"] = int(updated.Generation)
		output["restarted_at"] = restartedAt
	case "label_selector":
		selector := buildRestartPodsSelector(deployment, request.LabelSelector)
		if selector == "" {
			return app.ToolRunResult{}, k8sInvalidArgument("label_selector mode requires a selector that overlaps the deployment selector")
		}
		maxPods := request.MaxPods
		if maxPods <= 0 {
			maxPods = 1
		}
		pods, listErr := h.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if listErr != nil {
			return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s restart pods failed: %v", listErr), true)
		}
		affected := 0
		deletedPods := make([]string, 0, maxPods)
		for index := range pods.Items {
			if affected >= maxPods {
				break
			}
			pod := pods.Items[index]
			if deleteErr := h.client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); deleteErr != nil {
				return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s restart pods failed: %v", deleteErr), true)
			}
			affected++
			deletedPods = append(deletedPods, pod.Name)
		}
		output["pods_affected"] = affected
		output["deleted_pods"] = deletedPods
		output["rollout_revision"] = int(deployment.Generation)
	default:
		return app.ToolRunResult{}, k8sInvalidArgument("mode must be rollout_restart or label_selector")
	}

	summary := fmt.Sprintf("restarted pods for deployment %s/%s in %s mode", namespace, request.DeploymentName, mode)
	output[k8sDelivKeySummary] = summary
	output[k8sDelivKeyOutput] = summary
	output[k8sDelivKeyExitCode] = 0
	return k8sResult(output, "k8s-restart-pods-report.json"), nil
}

func (h *K8sCircuitBreakHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace     string `json:"namespace"`
		TargetService string `json:"target_service"`
		Downstream    string `json:"downstream"`
		TTLSeconds    int    `json:"ttl_seconds"`
	}{}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}
	if strings.TrimSpace(request.TargetService) == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("target_service is required")
	}
	if strings.TrimSpace(request.Downstream) == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("downstream is required")
	}
	if request.TTLSeconds < 60 || request.TTLSeconds > 1800 {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodePolicyDenied, Message: "ttl_out_of_bounds", Retryable: false}
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	targetService, err := h.client.CoreV1().Services(namespace).Get(ctx, request.TargetService, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeNotFound, Message: "target service not found", Retryable: false}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s circuit break failed: %v", err), true)
	}
	if len(targetService.Spec.Selector) == 0 {
		return app.ToolRunResult{}, k8sInvalidArgument("target service does not define a selector")
	}

	downstreamService, err := h.client.CoreV1().Services(namespace).Get(ctx, request.Downstream, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeNotFound, Message: "downstream service not found", Retryable: false}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s circuit break failed: %v", err), true)
	}
	if strings.TrimSpace(downstreamService.Spec.ClusterIP) == "" || downstreamService.Spec.ClusterIP == "None" {
		return app.ToolRunResult{}, k8sInvalidArgument("downstream service must expose a cluster IP")
	}

	policyID := circuitBreakPolicyID(namespace, request.TargetService, request.Downstream)
	expiresAt := h.now().Add(time.Duration(request.TTLSeconds) * time.Second).UTC()
	policy := buildCircuitBreakNetworkPolicy(namespace, policyID, targetService, downstreamService, expiresAt)

	existing, getErr := h.client.NetworkingV1().NetworkPolicies(namespace).Get(ctx, policyID, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(getErr):
		if _, createErr := h.client.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{}); createErr != nil {
			return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s circuit break failed: %v", createErr), true)
		}
	case getErr != nil:
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s circuit break failed: %v", getErr), true)
	default:
		policy.ResourceVersion = existing.ResourceVersion
		if _, updateErr := h.client.NetworkingV1().NetworkPolicies(namespace).Update(ctx, policy, metav1.UpdateOptions{}); updateErr != nil {
			return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s circuit break failed: %v", updateErr), true)
		}
	}

	h.schedulePolicyCleanup(namespace, policyID, time.Duration(request.TTLSeconds)*time.Second)

	summary := fmt.Sprintf("installed circuit break for %s/%s against %s until %s", namespace, request.TargetService, request.Downstream, expiresAt.Format(time.RFC3339))
	output := map[string]any{
		k8sDelivKeyNamespace: namespace,
		"target_service":     request.TargetService,
		"downstream":         request.Downstream,
		"policy_id":          policyID,
		"expires_at":         expiresAt.Format(time.RFC3339),
		"mesh_kind":          "networkpolicy",
		k8sDelivKeySummary:   summary,
		k8sDelivKeyOutput:    summary,
		k8sDelivKeyExitCode:  0,
	}
	return k8sResult(output, "k8s-circuit-break-report.json"), nil
}

func buildRestartPodsSelector(deployment *appsv1.Deployment, labelSelector string) string {
	base := strings.TrimSpace(metav1.FormatLabelSelector(deployment.Spec.Selector))
	extra := strings.TrimSpace(labelSelector)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "," + extra
	}
}

func circuitBreakPolicyID(namespace, targetService, downstream string) string {
	hash := sha256.Sum256([]byte(namespace + ":" + targetService + ":" + downstream))
	return fmt.Sprintf("circuit-break-%x", hash[:6])
}

func buildCircuitBreakNetworkPolicy(
	namespace, policyID string,
	targetService, downstreamService *corev1.Service,
	expiresAt time.Time,
) *networkingv1.NetworkPolicy {
	egressPeer := networkPolicyPeerAllowAllExcept(downstreamService.Spec.ClusterIP)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyID,
			Namespace: namespace,
			Annotations: map[string]string{
				"underpass.ai/target_service": targetService.Name,
				"underpass.ai/downstream":     downstreamService.Name,
				"underpass.ai/expires_at":     expiresAt.UTC().Format(time.RFC3339),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: targetService.Spec.Selector},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{egressPeer},
				},
			},
		},
	}
}

func networkPolicyPeerAllowAllExcept(clusterIP string) networkingv1.NetworkPolicyPeer {
	parsed := net.ParseIP(strings.TrimSpace(clusterIP))
	cidr := "0.0.0.0/0"
	except := []string{}
	if parsed != nil {
		if parsed.To4() != nil {
			except = []string{parsed.String() + "/32"}
		} else {
			cidr = "::/0"
			except = []string{parsed.String() + "/128"}
		}
	}
	return networkingv1.NetworkPolicyPeer{
		IPBlock: &networkingv1.IPBlock{
			CIDR:   cidr,
			Except: except,
		},
	}
}

func (h *K8sCircuitBreakHandler) schedulePolicyCleanup(namespace, policyID string, ttl time.Duration) {
	h.mu.Lock()
	if existing := h.timers[policyID]; existing != nil {
		existing.Stop()
	}
	timer := time.AfterFunc(ttl, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.client.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, policyID, metav1.DeleteOptions{})

		h.mu.Lock()
		delete(h.timers, policyID)
		h.mu.Unlock()
	})
	h.timers[policyID] = timer
	h.mu.Unlock()
}
