//go:build k8s

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	k8sDeploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
	k8sDesiredReplicasAnnotation    = "deployment.kubernetes.io/desired-replicas"
	k8sPodTemplateHashLabel         = "pod-template-hash"
	k8sRolloutMinAge                = 60 * time.Second
)

type K8sGetReplicaSetsHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sRolloutPauseHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sRolloutUndoHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

func NewK8sGetReplicaSetsHandler(client kubernetes.Interface, defaultNamespace string) *K8sGetReplicaSetsHandler {
	return &K8sGetReplicaSetsHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sRolloutPauseHandler(client kubernetes.Interface, defaultNamespace string) *K8sRolloutPauseHandler {
	return &K8sRolloutPauseHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sRolloutUndoHandler(client kubernetes.Interface, defaultNamespace string) *K8sRolloutUndoHandler {
	return &K8sRolloutUndoHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func (h *K8sGetReplicaSetsHandler) Name() string {
	return "k8s.get_replicasets"
}

func (h *K8sRolloutPauseHandler) Name() string {
	return "k8s.rollout_pause"
}

func (h *K8sRolloutUndoHandler) Name() string {
	return "k8s.rollout_undo"
}

func (h *K8sGetReplicaSetsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
	}{}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	deploymentName := strings.TrimSpace(request.DeploymentName)
	replicaSets, runErr := listReplicaSetsForRequest(ctx, h.client, namespace, deploymentName)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	current := currentReplicaSet(nil, replicaSets)
	outputSets := make([]map[string]any, 0, len(replicaSets))
	for i := range replicaSets {
		isCurrent := current != nil && current.Name == replicaSets[i].Name
		outputSets = append(outputSets, replicaSetOutput(replicaSets[i], isCurrent))
	}

	summary := fmt.Sprintf("listed %d replicasets in namespace %s", len(outputSets), namespace)
	if deploymentName != "" {
		summary = fmt.Sprintf("listed %d replicasets for deployment %s/%s", len(outputSets), namespace, deploymentName)
	}

	output := map[string]any{
		"namespace":       namespace,
		"deployment_name": deploymentName,
		"replicasets":     outputSets,
		"summary":         summary,
		"output":          summary,
		"exit_code":       0,
	}
	return k8sResult(output, "k8s-get-replicasets-report.json"), nil
}

func (h *K8sRolloutPauseHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
	}{}
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
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeNotFound, Message: "deployment not found", Retryable: false}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s rollout pause failed: %v", err), true)
	}

	if deployment.Spec.Paused {
		return k8sRolloutPauseResult(namespace, deploymentName, deployment.Status.ObservedGeneration, false), nil
	}

	deployment.Spec.Paused = true
	updated, updateErr := h.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if updateErr != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s rollout pause failed: %v", updateErr), true)
	}

	return k8sRolloutPauseResult(namespace, deploymentName, updated.Status.ObservedGeneration, true), nil
}

func (h *K8sRolloutUndoHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace      string `json:"namespace"`
		DeploymentName string `json:"deployment_name"`
		ToRevision     int    `json:"to_revision"`
	}{}
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
	if request.ToRevision < 0 {
		return app.ToolRunResult{}, k8sInvalidArgument("to_revision must be >= 1 when provided")
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	deployment, err := h.client.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{Code: app.ErrorCodeNotFound, Message: "deployment not found", Retryable: false}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s rollout undo failed: %v", err), true)
	}

	replicaSets, runErr := listOwnedReplicaSets(ctx, h.client, namespace, deployment)
	if runErr != nil {
		return app.ToolRunResult{}, runErr
	}

	current := currentReplicaSet(deployment, replicaSets)
	if current != nil && !current.CreationTimestamp.IsZero() && time.Since(current.CreationTimestamp.Time) < k8sRolloutMinAge {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeRolloutTooYoung,
			Message:   "current rollout is younger than 60 seconds",
			Retryable: false,
		}
	}

	target, targetErr := targetRollbackReplicaSet(replicaSets, current, request.ToRevision)
	if targetErr != nil {
		return app.ToolRunResult{}, targetErr
	}

	previousRevision := 0
	if current != nil {
		previousRevision = replicaSetRevision(current)
	}
	targetRevision := replicaSetRevision(target)
	if current != nil && current.Name == target.Name {
		return k8sRolloutUndoResult(namespace, deploymentName, previousRevision, targetRevision, false, deployment.Status.ObservedGeneration), nil
	}

	deployment.Spec.Template = *target.Spec.Template.DeepCopy()
	updated, updateErr := h.client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if updateErr != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s rollout undo failed: %v", updateErr), true)
	}

	return k8sRolloutUndoResult(namespace, deploymentName, previousRevision, targetRevision, true, updated.Status.ObservedGeneration), nil
}

func listReplicaSetsForRequest(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	deploymentName string,
) ([]appsv1.ReplicaSet, *domain.Error) {
	if deploymentName == "" {
		list, err := client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, k8sExecutionFailed(fmt.Sprintf("k8s get replicasets failed: %v", err), true)
		}
		items := append([]appsv1.ReplicaSet(nil), list.Items...)
		sortReplicaSets(items)
		return items, nil
	}

	deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, &domain.Error{Code: app.ErrorCodeNotFound, Message: "deployment not found", Retryable: false}
	}
	if err != nil {
		return nil, k8sExecutionFailed(fmt.Sprintf("k8s get deployment failed: %v", err), true)
	}
	return listOwnedReplicaSets(ctx, client, namespace, deployment)
}

func listOwnedReplicaSets(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	deployment *appsv1.Deployment,
) ([]appsv1.ReplicaSet, *domain.Error) {
	options := metav1.ListOptions{}
	if deployment != nil && deployment.Spec.Selector != nil && len(deployment.Spec.Selector.MatchLabels) > 0 {
		options.LabelSelector = labels.Set(deployment.Spec.Selector.MatchLabels).String()
	}

	list, err := client.AppsV1().ReplicaSets(namespace).List(ctx, options)
	if err != nil {
		return nil, k8sExecutionFailed(fmt.Sprintf("k8s get replicasets failed: %v", err), true)
	}

	items := make([]appsv1.ReplicaSet, 0, len(list.Items))
	for _, item := range list.Items {
		if deployment == nil || replicaSetOwnedByDeployment(item, deployment) {
			items = append(items, item)
		}
	}
	sortReplicaSets(items)
	return items, nil
}

func replicaSetOwnedByDeployment(replicaSet appsv1.ReplicaSet, deployment *appsv1.Deployment) bool {
	if deployment == nil {
		return true
	}
	for _, owner := range replicaSet.OwnerReferences {
		if strings.TrimSpace(owner.Kind) != "Deployment" || strings.TrimSpace(owner.Name) != deployment.Name {
			continue
		}
		if len(owner.UID) == 0 || len(deployment.UID) == 0 || owner.UID == deployment.UID {
			return true
		}
	}
	return false
}

func sortReplicaSets(items []appsv1.ReplicaSet) {
	sort.Slice(items, func(i, j int) bool {
		leftRevision := replicaSetRevision(&items[i])
		rightRevision := replicaSetRevision(&items[j])
		if leftRevision != rightRevision {
			return leftRevision > rightRevision
		}
		if !items[i].CreationTimestamp.Equal(&items[j].CreationTimestamp) {
			return items[i].CreationTimestamp.Time.After(items[j].CreationTimestamp.Time)
		}
		return items[i].Name < items[j].Name
	})
}

func replicaSetRevision(replicaSet *appsv1.ReplicaSet) int {
	if replicaSet == nil {
		return 0
	}
	raw := strings.TrimSpace(replicaSet.Annotations[k8sDeploymentRevisionAnnotation])
	if raw == "" {
		return 0
	}
	revision, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return revision
}

func currentReplicaSet(deployment *appsv1.Deployment, replicaSets []appsv1.ReplicaSet) *appsv1.ReplicaSet {
	if len(replicaSets) == 0 {
		return nil
	}
	if deployment != nil {
		currentHash := strings.TrimSpace(deployment.Spec.Template.Labels[k8sPodTemplateHashLabel])
		if currentHash != "" {
			for i := range replicaSets {
				if replicaSetTemplateHash(&replicaSets[i]) == currentHash {
					return &replicaSets[i]
				}
			}
		}
		for i := range replicaSets {
			if apiequality.Semantic.DeepEqual(replicaSets[i].Spec.Template, deployment.Spec.Template) {
				return &replicaSets[i]
			}
		}
	}
	return &replicaSets[0]
}

func replicaSetTemplateHash(replicaSet *appsv1.ReplicaSet) string {
	if replicaSet == nil {
		return ""
	}
	if value := strings.TrimSpace(replicaSet.Labels[k8sPodTemplateHashLabel]); value != "" {
		return value
	}
	return strings.TrimSpace(replicaSet.Spec.Template.Labels[k8sPodTemplateHashLabel])
}

func targetRollbackReplicaSet(
	replicaSets []appsv1.ReplicaSet,
	current *appsv1.ReplicaSet,
	toRevision int,
) (*appsv1.ReplicaSet, *domain.Error) {
	if len(replicaSets) == 0 {
		return nil, &domain.Error{
			Code:      app.ErrorCodeNoPreviousHealthyReplicaSet,
			Message:   "deployment has no previous healthy replicaset",
			Retryable: false,
		}
	}

	if toRevision > 0 {
		for i := range replicaSets {
			if replicaSetRevision(&replicaSets[i]) != toRevision {
				continue
			}
			if current != nil && replicaSets[i].Name == current.Name {
				return current, nil
			}
			if replicaSetHealthyRollbackTarget(&replicaSets[i]) {
				return &replicaSets[i], nil
			}
			return nil, &domain.Error{
				Code:      app.ErrorCodeNoPreviousHealthyReplicaSet,
				Message:   fmt.Sprintf("revision %d is not a healthy rollback target", toRevision),
				Retryable: false,
			}
		}
		return nil, k8sInvalidArgument(fmt.Sprintf("revision %d not found", toRevision))
	}

	for i := range replicaSets {
		if current != nil && replicaSets[i].Name == current.Name {
			continue
		}
		if replicaSetHealthyRollbackTarget(&replicaSets[i]) {
			return &replicaSets[i], nil
		}
	}

	return nil, &domain.Error{
		Code:      app.ErrorCodeNoPreviousHealthyReplicaSet,
		Message:   "deployment has no previous healthy replicaset",
		Retryable: false,
	}
}

func replicaSetHealthyRollbackTarget(replicaSet *appsv1.ReplicaSet) bool {
	if replicaSet == nil {
		return false
	}
	if replicaSet.Status.ReadyReplicas > 0 || replicaSet.Status.AvailableReplicas > 0 {
		return true
	}
	return replicaSetDesiredReplicas(replicaSet) > 0
}

func replicaSetDesiredReplicas(replicaSet *appsv1.ReplicaSet) int {
	if replicaSet == nil {
		return 0
	}
	raw := strings.TrimSpace(replicaSet.Annotations[k8sDesiredReplicasAnnotation])
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func replicaSetOutput(replicaSet appsv1.ReplicaSet, current bool) map[string]any {
	return map[string]any{
		"name":               replicaSet.Name,
		"revision":           replicaSetRevision(&replicaSet),
		"replicas":           derefInt32(replicaSet.Spec.Replicas),
		"ready_replicas":     replicaSet.Status.ReadyReplicas,
		"available_replicas": replicaSet.Status.AvailableReplicas,
		"pod_template_hash":  replicaSetTemplateHash(&replicaSet),
		"current":            current,
	}
}

func k8sRolloutPauseResult(namespace, deploymentName string, observedGeneration int64, updated bool) app.ToolRunResult {
	summary := fmt.Sprintf("paused deployment %s/%s", namespace, deploymentName)
	if !updated {
		summary = fmt.Sprintf("deployment %s/%s is already paused", namespace, deploymentName)
	}
	output := map[string]any{
		"namespace":           namespace,
		"deployment_name":     deploymentName,
		"paused":              true,
		"observed_generation": observedGeneration,
		"summary":             summary,
		"output":              summary,
		"exit_code":           0,
	}
	return k8sResult(output, "k8s-rollout-pause-report.json")
}

func k8sRolloutUndoResult(namespace, deploymentName string, previousRevision, targetRevision int, rolledBack bool, observedGeneration int64) app.ToolRunResult {
	summary := fmt.Sprintf("rolled back deployment %s/%s to revision %d", namespace, deploymentName, targetRevision)
	if !rolledBack {
		summary = fmt.Sprintf("deployment %s/%s is already at revision %d", namespace, deploymentName, targetRevision)
	}
	output := map[string]any{
		"namespace":           namespace,
		"deployment_name":     deploymentName,
		"previous_revision":   previousRevision,
		"target_revision":     targetRevision,
		"rolled_back":         rolledBack,
		"observed_generation": observedGeneration,
		"summary":             summary,
		"output":              summary,
		"exit_code":           0,
	}
	return k8sResult(output, "k8s-rollout-undo-report.json")
}
