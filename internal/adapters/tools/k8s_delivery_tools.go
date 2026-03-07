package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	k8sApplyManifestMaxBytes          = 256 * 1024
	k8sApplyManifestMaxObjectsDefault = 10
	k8sApplyManifestMaxObjectsLimit   = 20
	k8sRolloutDefaultTimeoutSeconds   = 120
	k8sRolloutDefaultPollIntervalMS   = 1000
	k8sRolloutMinPollIntervalMS       = 100
	k8sRolloutMaxPollIntervalMS       = 10000
	k8sOperationCreated               = "created"
	k8sOperationUpdated               = "updated"
	k8sDelivKeyNamespace              = "namespace"
	k8sDelivKeyOperation              = "operation"
	k8sDelivKeyDeployment             = "deployment_name"
	k8sDelivKeyAPIVersion             = "api_version"
	k8sDelivKeySummary                = "summary"
	k8sDelivKeyOutput                 = "output"
	k8sDelivKeyExitCode               = "exit_code"
	k8sDelivStatusCompleted           = "completed"
	k8sFmtApplyConfigmapFailed        = "k8s apply configmap failed: %v"
	k8sFmtApplyDeploymentFailed       = "k8s apply deployment failed: %v"
	k8sFmtApplyServiceFailed          = "k8s apply service failed: %v"
)

var k8sApplyAllowedKinds = map[string]struct{}{
	"configmap":  {},
	"deployment": {},
	"service":    {},
}

type K8sApplyManifestHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sRolloutStatusHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sRestartDeploymentHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type k8sManifestDocument struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	RawJSON    []byte
}

func NewK8sApplyManifestHandler(client kubernetes.Interface, defaultNamespace string) *K8sApplyManifestHandler {
	return &K8sApplyManifestHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sRolloutStatusHandler(client kubernetes.Interface, defaultNamespace string) *K8sRolloutStatusHandler {
	return &K8sRolloutStatusHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sRestartDeploymentHandler(client kubernetes.Interface, defaultNamespace string) *K8sRestartDeploymentHandler {
	return &K8sRestartDeploymentHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func (h *K8sApplyManifestHandler) Name() string {
	return "k8s.apply_manifest"
}

func (h *K8sRolloutStatusHandler) Name() string {
	return "k8s.rollout_status"
}

func (h *K8sRestartDeploymentHandler) Name() string {
	return "k8s.restart_deployment"
}

func (h *K8sApplyManifestHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace  string `json:"namespace"`
		Manifest   string `json:"manifest"`
		DryRun     bool   `json:"dry_run"`
		MaxObjects int    `json:"max_objects"`
	}{
		MaxObjects: k8sApplyManifestMaxObjectsDefault,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}

	manifest := strings.TrimSpace(request.Manifest)
	if manifest == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("manifest is required")
	}
	if len([]byte(manifest)) > k8sApplyManifestMaxBytes {
		return app.ToolRunResult{}, k8sInvalidArgument(
			fmt.Sprintf("manifest exceeds %d bytes", k8sApplyManifestMaxBytes),
		)
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	maxObjects := clampInt(
		request.MaxObjects,
		1,
		k8sApplyManifestMaxObjectsLimit,
		k8sApplyManifestMaxObjectsDefault,
	)
	documents, decodeErr := decodeK8sManifestDocuments(manifest, maxObjects)
	if decodeErr != nil {
		return app.ToolRunResult{}, decodeErr
	}
	if len(documents) == 0 {
		return app.ToolRunResult{}, k8sInvalidArgument("manifest does not contain Kubernetes objects")
	}

	resources, createdCount, updatedCount, policyErr := h.applyDocuments(ctx, namespace, request.DryRun, documents)
	if policyErr != nil {
		return app.ToolRunResult{}, policyErr
	}

	summary := fmt.Sprintf("applied %d resources in namespace %s", len(resources), namespace)
	output := map[string]any{
		k8sDelivKeyNamespace: namespace,
		"dry_run":       request.DryRun,
		"applied_count": len(resources),
		"created_count": createdCount,
		"updated_count": updatedCount,
		"resources":     resources,
		k8sDelivKeySummary: summary,
		k8sDelivKeyOutput: summary,
		k8sDelivKeyExitCode: 0,
	}
	return k8sResult(output, "k8s-apply-manifest-report.json"), nil
}

func (h *K8sApplyManifestHandler) applyDocuments(
	ctx context.Context,
	namespace string,
	dryRun bool,
	documents []k8sManifestDocument,
) ([]map[string]any, int, int, *domain.Error) {
	resources := make([]map[string]any, 0, len(documents))
	createdCount := 0
	updatedCount := 0
	for _, document := range documents {
		if !k8sManifestKindAllowed(document.Kind) {
			return nil, 0, 0, &domain.Error{
				Code:      app.ErrorCodePolicyDenied,
				Message:   fmt.Sprintf("manifest kind not allowed: %s", document.Kind),
				Retryable: false,
			}
		}
		if !k8sManifestNamespaceAllowed(document.Namespace, namespace) {
			return nil, 0, 0, &domain.Error{
				Code:      app.ErrorCodePolicyDenied,
				Message:   "manifest namespace must match requested namespace",
				Retryable: false,
			}
		}
		resource, applyErr := h.applyDocument(ctx, namespace, dryRun, document)
		if applyErr != nil {
			return nil, 0, 0, applyErr
		}
		if asString(resource[k8sDelivKeyOperation]) == k8sOperationCreated {
			createdCount++
		}
		if asString(resource[k8sDelivKeyOperation]) == k8sOperationUpdated {
			updatedCount++
		}
		resources = append(resources, resource)
	}
	return resources, createdCount, updatedCount, nil
}

func (h *K8sApplyManifestHandler) applyDocument(
	ctx context.Context,
	namespace string,
	dryRun bool,
	document k8sManifestDocument,
) (map[string]any, *domain.Error) {
	switch strings.ToLower(strings.TrimSpace(document.Kind)) {
	case "configmap":
		operation, err := h.applyConfigMap(ctx, namespace, dryRun, document)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			k8sDelivKeyAPIVersion: document.APIVersion,
			"kind":        "ConfigMap",
			"name":        document.Name,
			k8sDelivKeyNamespace: namespace,
			k8sDelivKeyOperation: operation,
		}, nil
	case "deployment":
		operation, err := h.applyDeployment(ctx, namespace, dryRun, document)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			k8sDelivKeyAPIVersion: document.APIVersion,
			"kind":        "Deployment",
			"name":        document.Name,
			k8sDelivKeyNamespace: namespace,
			k8sDelivKeyOperation: operation,
		}, nil
	case "service":
		operation, err := h.applyService(ctx, namespace, dryRun, document)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			k8sDelivKeyAPIVersion: document.APIVersion,
			"kind":        "Service",
			"name":        document.Name,
			k8sDelivKeyNamespace: namespace,
			k8sDelivKeyOperation: operation,
		}, nil
	default:
		return nil, &domain.Error{
			Code:      app.ErrorCodePolicyDenied,
			Message:   fmt.Sprintf("manifest kind not allowed: %s", document.Kind),
			Retryable: false,
		}
	}
}

func (h *K8sApplyManifestHandler) applyConfigMap(
	ctx context.Context,
	namespace string,
	dryRun bool,
	document k8sManifestDocument,
) (string, *domain.Error) {
	var configMap corev1.ConfigMap
	if err := json.Unmarshal(document.RawJSON, &configMap); err != nil {
		return "", k8sInvalidArgument("manifest configmap is invalid")
	}
	configMap.Namespace = namespace

	createOptions := metav1.CreateOptions{}
	updateOptions := metav1.UpdateOptions{}
	if dryRun {
		createOptions.DryRun = []string{metav1.DryRunAll}
		updateOptions.DryRun = []string{metav1.DryRunAll}
	}

	existing, err := h.client.CoreV1().ConfigMaps(namespace).Get(ctx, configMap.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, createErr := h.client.CoreV1().ConfigMaps(namespace).Create(ctx, &configMap, createOptions); createErr != nil {
			return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyConfigmapFailed, createErr), true)
		}
		return k8sOperationCreated, nil
	}
	if err != nil {
		return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyConfigmapFailed, err), true)
	}

	configMap.ResourceVersion = existing.ResourceVersion
	if _, updateErr := h.client.CoreV1().ConfigMaps(namespace).Update(ctx, &configMap, updateOptions); updateErr != nil {
		return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyConfigmapFailed, updateErr), true)
	}
	return k8sOperationUpdated, nil
}

func (h *K8sApplyManifestHandler) applyDeployment(
	ctx context.Context,
	namespace string,
	dryRun bool,
	document k8sManifestDocument,
) (string, *domain.Error) {
	var deployment appsv1.Deployment
	if err := json.Unmarshal(document.RawJSON, &deployment); err != nil {
		return "", k8sInvalidArgument("manifest deployment is invalid")
	}
	deployment.Namespace = namespace

	createOptions := metav1.CreateOptions{}
	updateOptions := metav1.UpdateOptions{}
	if dryRun {
		createOptions.DryRun = []string{metav1.DryRunAll}
		updateOptions.DryRun = []string{metav1.DryRunAll}
	}

	existing, err := h.client.AppsV1().Deployments(namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, createErr := h.client.AppsV1().Deployments(namespace).Create(ctx, &deployment, createOptions); createErr != nil {
			return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyDeploymentFailed, createErr), true)
		}
		return k8sOperationCreated, nil
	}
	if err != nil {
		return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyDeploymentFailed, err), true)
	}

	deployment.ResourceVersion = existing.ResourceVersion
	if _, updateErr := h.client.AppsV1().Deployments(namespace).Update(ctx, &deployment, updateOptions); updateErr != nil {
		return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyDeploymentFailed, updateErr), true)
	}
	return k8sOperationUpdated, nil
}

func (h *K8sApplyManifestHandler) applyService(
	ctx context.Context,
	namespace string,
	dryRun bool,
	document k8sManifestDocument,
) (string, *domain.Error) {
	var service corev1.Service
	if err := json.Unmarshal(document.RawJSON, &service); err != nil {
		return "", k8sInvalidArgument("manifest service is invalid")
	}
	service.Namespace = namespace

	createOptions := metav1.CreateOptions{}
	updateOptions := metav1.UpdateOptions{}
	if dryRun {
		createOptions.DryRun = []string{metav1.DryRunAll}
		updateOptions.DryRun = []string{metav1.DryRunAll}
	}

	existing, err := h.client.CoreV1().Services(namespace).Get(ctx, service.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, createErr := h.client.CoreV1().Services(namespace).Create(ctx, &service, createOptions); createErr != nil {
			return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyServiceFailed, createErr), true)
		}
		return k8sOperationCreated, nil
	}
	if err != nil {
		return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyServiceFailed, err), true)
	}

	preserveServiceImmutableFields(&service, existing)
	service.ResourceVersion = existing.ResourceVersion
	if _, updateErr := h.client.CoreV1().Services(namespace).Update(ctx, &service, updateOptions); updateErr != nil {
		return "", k8sExecutionFailed(fmt.Sprintf(k8sFmtApplyServiceFailed, updateErr), true)
	}
	return k8sOperationUpdated, nil
}

func (h *K8sRolloutStatusHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		PollIntervalMS int    `json:"poll_interval_ms"`
	}{
		TimeoutSeconds: k8sRolloutDefaultTimeoutSeconds,
		PollIntervalMS: k8sRolloutDefaultPollIntervalMS,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}
	deploymentName := strings.TrimSpace(request.DeploymentName)
	if deploymentName == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("deployment_name is required")
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	timeout := time.Duration(
		clampInt(request.TimeoutSeconds, 1, 1800, k8sRolloutDefaultTimeoutSeconds),
	) * time.Second
	pollInterval := time.Duration(
		clampInt(
			request.PollIntervalMS,
			k8sRolloutMinPollIntervalMS,
			k8sRolloutMaxPollIntervalMS,
			k8sRolloutDefaultPollIntervalMS,
		),
	) * time.Millisecond

	started := time.Now()
	snapshot, waitErr := waitForDeploymentRollout(
		ctx,
		h.client,
		namespace,
		deploymentName,
		timeout,
		pollInterval,
	)
	if waitErr != nil {
		return app.ToolRunResult{}, waitErr
	}

	summary := fmt.Sprintf("deployment %s/%s rollout is complete", namespace, deploymentName)
	output := map[string]any{
		k8sDelivKeyNamespace: namespace,
		k8sDelivKeyDeployment: deploymentName,
		"status":          k8sDelivStatusCompleted,
		"duration_ms":     int(time.Since(started).Milliseconds()),
		"rollout":         snapshot,
		k8sDelivKeySummary: summary,
		k8sDelivKeyOutput: summary,
		k8sDelivKeyExitCode: 0,
	}
	return k8sResult(output, "k8s-rollout-status-report.json"), nil
}

func (h *K8sRestartDeploymentHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
		WaitForRollout bool   `json:"wait_for_rollout"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		PollIntervalMS int    `json:"poll_interval_ms"`
	}{
		TimeoutSeconds: k8sRolloutDefaultTimeoutSeconds,
		PollIntervalMS: k8sRolloutDefaultPollIntervalMS,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}
	deploymentName := strings.TrimSpace(request.DeploymentName)
	if deploymentName == "" {
		return app.ToolRunResult{}, k8sInvalidArgument("deployment_name is required")
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	deployment, err := h.client.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeNotFound,
			Message:   "deployment not found",
			Retryable: false,
		}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(
			fmt.Sprintf("k8s restart deployment failed: %v", err),
			true,
		)
	}

	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	previousRestartedAt := strings.TrimSpace(deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])
	restartedAt := time.Now().UTC().Format(time.RFC3339)
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = restartedAt

	updated, updateErr := h.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if updateErr != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(
			fmt.Sprintf("k8s restart deployment failed: %v", updateErr),
			true,
		)
	}

	output := map[string]any{
		k8sDelivKeyNamespace: namespace,
		k8sDelivKeyDeployment: deploymentName,
		"restarted_at":          restartedAt,
		"previous_restarted_at": previousRestartedAt,
		"generation":            updated.Generation,
		"observed_generation":   updated.Status.ObservedGeneration,
	}

	if request.WaitForRollout {
		timeout := time.Duration(
			clampInt(request.TimeoutSeconds, 1, 1800, k8sRolloutDefaultTimeoutSeconds),
		) * time.Second
		pollInterval := time.Duration(
			clampInt(
				request.PollIntervalMS,
				k8sRolloutMinPollIntervalMS,
				k8sRolloutMaxPollIntervalMS,
				k8sRolloutDefaultPollIntervalMS,
			),
		) * time.Millisecond

		snapshot, waitErr := waitForDeploymentRollout(
			ctx,
			h.client,
			namespace,
			deploymentName,
			timeout,
			pollInterval,
		)
		if waitErr != nil {
			return app.ToolRunResult{}, waitErr
		}
		output["rollout"] = snapshot
		output["rollout_status"] = k8sDelivStatusCompleted
	} else {
		output["rollout_status"] = "pending"
	}

	summary := fmt.Sprintf("restarted deployment %s/%s", namespace, deploymentName)
	output[k8sDelivKeySummary] = summary
	output[k8sDelivKeyOutput] = summary
	output[k8sDelivKeyExitCode] = 0
	return k8sResult(output, "k8s-restart-deployment-report.json"), nil
}

func decodeK8sManifestDocuments(raw string, maxObjects int) ([]k8sManifestDocument, *domain.Error) {
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	documents := make([]k8sManifestDocument, 0, maxObjects)

	for {
		payload := map[string]any{}
		err := decoder.Decode(&payload)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, k8sInvalidArgument("manifest is invalid YAML/JSON")
		}
		if len(payload) == 0 {
			continue
		}
		if len(documents) >= maxObjects {
			return nil, k8sInvalidArgument(
				fmt.Sprintf("manifest exceeds max_objects limit (%d)", maxObjects),
			)
		}
		doc, docErr := parseK8sManifestPayload(payload)
		if docErr != nil {
			return nil, docErr
		}
		documents = append(documents, doc)
	}

	return documents, nil
}

func parseK8sManifestPayload(payload map[string]any) (k8sManifestDocument, *domain.Error) {
	rawJSON, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return k8sManifestDocument{}, k8sInvalidArgument("manifest object could not be decoded")
	}

	header := struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}{}
	if unmarshalErr := json.Unmarshal(rawJSON, &header); unmarshalErr != nil {
		return k8sManifestDocument{}, k8sInvalidArgument("manifest object is invalid")
	}

	kind := strings.TrimSpace(header.Kind)
	name := strings.TrimSpace(header.Metadata.Name)
	if kind == "" {
		return k8sManifestDocument{}, k8sInvalidArgument("manifest kind is required")
	}
	if name == "" {
		return k8sManifestDocument{}, k8sInvalidArgument("manifest metadata.name is required")
	}

	return k8sManifestDocument{
		APIVersion: strings.TrimSpace(header.APIVersion),
		Kind:       kind,
		Name:       name,
		Namespace:  strings.TrimSpace(header.Metadata.Namespace),
		RawJSON:    rawJSON,
	}, nil
}

func k8sManifestKindAllowed(kind string) bool {
	_, ok := k8sApplyAllowedKinds[strings.ToLower(strings.TrimSpace(kind))]
	return ok
}

func k8sManifestNamespaceAllowed(documentNamespace, requestedNamespace string) bool {
	documentNamespace = strings.TrimSpace(documentNamespace)
	if documentNamespace == "" {
		return true
	}
	return documentNamespace == strings.TrimSpace(requestedNamespace)
}

func preserveServiceImmutableFields(service, existing *corev1.Service) {
	if service == nil || existing == nil {
		return
	}
	if service.Spec.ClusterIP == "" {
		service.Spec.ClusterIP = existing.Spec.ClusterIP
	}
	if len(service.Spec.ClusterIPs) == 0 && len(existing.Spec.ClusterIPs) > 0 {
		service.Spec.ClusterIPs = append([]string(nil), existing.Spec.ClusterIPs...)
	}
	if len(service.Spec.IPFamilies) == 0 && len(existing.Spec.IPFamilies) > 0 {
		service.Spec.IPFamilies = append([]corev1.IPFamily(nil), existing.Spec.IPFamilies...)
	}
	if service.Spec.IPFamilyPolicy == nil {
		service.Spec.IPFamilyPolicy = existing.Spec.IPFamilyPolicy
	}
	if service.Spec.HealthCheckNodePort == 0 {
		service.Spec.HealthCheckNodePort = existing.Spec.HealthCheckNodePort
	}
	restoreNodePorts(service, existing)
}

func restoreNodePorts(service, existing *corev1.Service) {
	existingNodePortByKey := buildExistingNodePortIndex(existing.Spec.Ports)
	for index := range service.Spec.Ports {
		if service.Spec.Ports[index].NodePort != 0 {
			continue
		}
		key := servicePortKey(service.Spec.Ports[index])
		if key == "" {
			continue
		}
		if nodePort, found := existingNodePortByKey[key]; found {
			service.Spec.Ports[index].NodePort = nodePort
		}
	}
}

func buildExistingNodePortIndex(ports []corev1.ServicePort) map[string]int32 {
	index := map[string]int32{}
	for _, port := range ports {
		key := servicePortKey(port)
		if key == "" || port.NodePort == 0 {
			continue
		}
		index[key] = port.NodePort
	}
	return index
}

func servicePortKey(port corev1.ServicePort) string {
	name := strings.TrimSpace(port.Name)
	protocol := strings.TrimSpace(string(port.Protocol))
	if protocol == "" {
		protocol = string(corev1.ProtocolTCP)
	}
	return fmt.Sprintf("%s|%d|%s", name, port.Port, protocol)
}

func waitForDeploymentRollout(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, deploymentName string,
	timeout time.Duration,
	pollInterval time.Duration,
) (map[string]any, *domain.Error) {
	deadline := time.Now().Add(timeout)
	lastSnapshot := map[string]any{}

	for {
		deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, &domain.Error{
				Code:      app.ErrorCodeNotFound,
				Message:   "deployment not found",
				Retryable: false,
			}
		}
		if err != nil {
			return nil, k8sExecutionFailed(
				fmt.Sprintf("k8s rollout status failed: %v", err),
				true,
			)
		}

		snapshot, completed := evaluateDeploymentRollout(deployment)
		lastSnapshot = snapshot
		if completed {
			return snapshot, nil
		}
		if time.Now().After(deadline) {
			return lastSnapshot, &domain.Error{
				Code:      app.ErrorCodeTimeout,
				Message:   "deployment rollout timeout exceeded",
				Retryable: true,
			}
		}

		select {
		case <-ctx.Done():
			return lastSnapshot, &domain.Error{
				Code:      app.ErrorCodeTimeout,
				Message:   "deployment rollout canceled",
				Retryable: true,
			}
		case <-time.After(pollInterval):
		}
	}
}

func evaluateDeploymentRollout(deployment *appsv1.Deployment) (map[string]any, bool) {
	desired := int(derefInt32(deployment.Spec.Replicas))
	updated := int(deployment.Status.UpdatedReplicas)
	ready := int(deployment.Status.ReadyReplicas)
	available := int(deployment.Status.AvailableReplicas)
	unavailable := int(deployment.Status.UnavailableReplicas)
	observedGeneration := deployment.Status.ObservedGeneration
	generation := deployment.Generation

	conditions := make([]map[string]any, 0, len(deployment.Status.Conditions))
	for _, condition := range deployment.Status.Conditions {
		conditions = append(conditions, map[string]any{
			"type":               string(condition.Type),
			"status":             string(condition.Status),
			"reason":             condition.Reason,
			"message":            condition.Message,
			"last_update_time":   condition.LastUpdateTime.UTC().Format(time.RFC3339),
			"last_transition_at": condition.LastTransitionTime.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(conditions, func(i, j int) bool {
		return asString(conditions[i]["type"]) < asString(conditions[j]["type"])
	})

	completed := observedGeneration >= generation &&
		updated >= desired &&
		ready >= desired &&
		available >= desired &&
		unavailable == 0

	return map[string]any{
		"generation":           generation,
		"observed_generation":  observedGeneration,
		"desired_replicas":     desired,
		"updated_replicas":     updated,
		"ready_replicas":       ready,
		"available_replicas":   available,
		"unavailable_replicas": unavailable,
		"conditions":           conditions,
		k8sDelivStatusCompleted: completed,
	}, completed
}

func k8sInvalidArgument(message string) *domain.Error {
	return &domain.Error{
		Code:      app.ErrorCodeInvalidArgument,
		Message:   message,
		Retryable: false,
	}
}

func k8sExecutionFailed(message string, retryable bool) *domain.Error {
	return &domain.Error{
		Code:      app.ErrorCodeExecutionFailed,
		Message:   message,
		Retryable: retryable,
	}
}
