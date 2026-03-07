package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

type K8sGetPodsHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sGetServicesHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sGetDeploymentsHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sGetImagesHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

type K8sGetLogsHandler struct {
	client           kubernetes.Interface
	defaultNamespace string
}

func NewK8sGetPodsHandler(client kubernetes.Interface, defaultNamespace string) *K8sGetPodsHandler {
	return &K8sGetPodsHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sGetServicesHandler(client kubernetes.Interface, defaultNamespace string) *K8sGetServicesHandler {
	return &K8sGetServicesHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sGetDeploymentsHandler(client kubernetes.Interface, defaultNamespace string) *K8sGetDeploymentsHandler {
	return &K8sGetDeploymentsHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sGetImagesHandler(client kubernetes.Interface, defaultNamespace string) *K8sGetImagesHandler {
	return &K8sGetImagesHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func NewK8sGetLogsHandler(client kubernetes.Interface, defaultNamespace string) *K8sGetLogsHandler {
	return &K8sGetLogsHandler{client: client, defaultNamespace: strings.TrimSpace(defaultNamespace)}
}

func (h *K8sGetPodsHandler) Name() string {
	return "k8s.get_pods"
}

func (h *K8sGetServicesHandler) Name() string {
	return "k8s.get_services"
}

func (h *K8sGetDeploymentsHandler) Name() string {
	return "k8s.get_deployments"
}

func (h *K8sGetImagesHandler) Name() string {
	return "k8s.get_images"
}

func (h *K8sGetLogsHandler) Name() string {
	return "k8s.get_logs"
}

func (h *K8sGetPodsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace         string `json:"namespace"`
		LabelSelector     string `json:"label_selector"`
		FieldSelector     string `json:"field_selector"`
		MaxPods           int    `json:"max_pods"`
		IncludeContainers bool   `json:"include_containers"`
		IncludeLabels     bool   `json:"include_labels"`
	}{
		MaxPods: 100,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	list, runErr := h.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.TrimSpace(request.LabelSelector),
		FieldSelector: strings.TrimSpace(request.FieldSelector),
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("k8s get pods failed: %v", runErr),
			Retryable: true,
		}
	}

	maxPods := clampInt(request.MaxPods, 1, 500, 100)
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	items, truncated := truncatePods(list.Items, maxPods)

	pods := make([]map[string]any, 0, len(items))
	for _, pod := range items {
		restartCount := int32(0)
		readyContainers := 0
		for _, status := range pod.Status.ContainerStatuses {
			restartCount += status.RestartCount
			if status.Ready {
				readyContainers++
			}
		}

		entry := map[string]any{
			"name":             pod.Name,
			"namespace":        pod.Namespace,
			"phase":            string(pod.Status.Phase),
			"node_name":        pod.Spec.NodeName,
			"pod_ip":           pod.Status.PodIP,
			"host_ip":          pod.Status.HostIP,
			"qos_class":        string(pod.Status.QOSClass),
			"ready_containers": readyContainers,
			"total_containers": len(pod.Spec.Containers),
			"restart_count":    restartCount,
			"created_at":       pod.CreationTimestamp.UTC().Format(time.RFC3339),
		}
		if request.IncludeLabels {
			entry["labels"] = mapStringMap(pod.Labels)
		}
		if request.IncludeContainers {
			entry["containers"] = buildPodContainerOutput(pod)
		}
		pods = append(pods, entry)
	}

	summary := fmt.Sprintf("listed %d pods from namespace %s", len(pods), namespace)
	output := map[string]any{
		"namespace":          namespace,
		"label_selector":     strings.TrimSpace(request.LabelSelector),
		"field_selector":     strings.TrimSpace(request.FieldSelector),
		"source":             "k8s_sdk",
		"count":              len(pods),
		"truncated":          truncated,
		"include_containers": request.IncludeContainers,
		"include_labels":     request.IncludeLabels,
		"pods":               pods,
		"summary":            summary,
		"output":             summary,
		"exit_code":          0,
	}
	return k8sResult(output, "k8s-get-pods-report.json"), nil
}

func (h *K8sGetServicesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace     string `json:"namespace"`
		LabelSelector string `json:"label_selector"`
		FieldSelector string `json:"field_selector"`
		MaxServices   int    `json:"max_services"`
		IncludeLabels bool   `json:"include_labels"`
	}{
		MaxServices: 100,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	list, runErr := h.client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.TrimSpace(request.LabelSelector),
		FieldSelector: strings.TrimSpace(request.FieldSelector),
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("k8s get services failed: %v", runErr),
			Retryable: true,
		}
	}

	maxServices := clampInt(request.MaxServices, 1, 500, 100)
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	items, truncated := truncateServices(list.Items, maxServices)

	services := make([]map[string]any, 0, len(items))
	for _, service := range items {
		entry := buildServiceEntry(service, request.IncludeLabels)
		services = append(services, entry)
	}

	summary := fmt.Sprintf("listed %d services from namespace %s", len(services), namespace)
	output := map[string]any{
		"namespace":      namespace,
		"label_selector": strings.TrimSpace(request.LabelSelector),
		"field_selector": strings.TrimSpace(request.FieldSelector),
		"source":         "k8s_sdk",
		"count":          len(services),
		"truncated":      truncated,
		"include_labels": request.IncludeLabels,
		"services":       services,
		"summary":        summary,
		"output":         summary,
		"exit_code":      0,
	}
	return k8sResult(output, "k8s-get-services-report.json"), nil
}

func buildServiceEntry(service corev1.Service, includeLabels bool) map[string]any {
	ports := buildServicePorts(service.Spec.Ports)
	externalIPs := buildServiceExternalIPs(service)
	entry := map[string]any{
		"name":         service.Name,
		"namespace":    service.Namespace,
		"type":         string(service.Spec.Type),
		"cluster_ip":   service.Spec.ClusterIP,
		"external_ips": externalIPs,
		"selector":     mapStringMap(service.Spec.Selector),
		"ports":        ports,
		"created_at":   service.CreationTimestamp.UTC().Format(time.RFC3339),
	}
	if includeLabels {
		entry["labels"] = mapStringMap(service.Labels)
	}
	return entry
}

func buildServicePorts(specPorts []corev1.ServicePort) []map[string]any {
	ports := make([]map[string]any, 0, len(specPorts))
	for _, port := range specPorts {
		ports = append(ports, map[string]any{
			"name":        port.Name,
			"protocol":    string(port.Protocol),
			"port":        port.Port,
			"target_port": port.TargetPort.String(),
			"node_port":   port.NodePort,
		})
	}
	sort.Slice(ports, func(i, j int) bool {
		left := asString(ports[i]["name"])
		right := asString(ports[j]["name"])
		if left != right {
			return left < right
		}
		return asInt64(ports[i]["port"]) < asInt64(ports[j]["port"])
	})
	return ports
}

func buildServiceExternalIPs(service corev1.Service) []string {
	externalIPs := make([]string, 0, len(service.Spec.ExternalIPs)+len(service.Status.LoadBalancer.Ingress))
	externalIPs = append(externalIPs, service.Spec.ExternalIPs...)
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if strings.TrimSpace(ingress.IP) != "" {
			externalIPs = append(externalIPs, ingress.IP)
		}
		if strings.TrimSpace(ingress.Hostname) != "" {
			externalIPs = append(externalIPs, ingress.Hostname)
		}
	}
	sort.Strings(externalIPs)
	return externalIPs
}

func (h *K8sGetDeploymentsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace         string `json:"namespace"`
		LabelSelector     string `json:"label_selector"`
		FieldSelector     string `json:"field_selector"`
		MaxDeployments    int    `json:"max_deployments"`
		IncludeContainers bool   `json:"include_containers"`
		IncludeLabels     bool   `json:"include_labels"`
	}{
		MaxDeployments: 100,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	list, runErr := h.client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.TrimSpace(request.LabelSelector),
		FieldSelector: strings.TrimSpace(request.FieldSelector),
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("k8s get deployments failed: %v", runErr),
			Retryable: true,
		}
	}

	maxDeployments := clampInt(request.MaxDeployments, 1, 500, 100)
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	items, truncated := truncateDeployments(list.Items, maxDeployments)

	deployments := make([]map[string]any, 0, len(items))
	for _, deployment := range items {
		entry := map[string]any{
			"name":                 deployment.Name,
			"namespace":            deployment.Namespace,
			"strategy":             string(deployment.Spec.Strategy.Type),
			"replicas":             derefInt32(deployment.Spec.Replicas),
			"ready_replicas":       deployment.Status.ReadyReplicas,
			"updated_replicas":     deployment.Status.UpdatedReplicas,
			"available_replicas":   deployment.Status.AvailableReplicas,
			"unavailable_replicas": deployment.Status.UnavailableReplicas,
			"selector":             mapStringMap(deployment.Spec.Selector.MatchLabels),
			"created_at":           deployment.CreationTimestamp.UTC().Format(time.RFC3339),
		}
		if request.IncludeLabels {
			entry["labels"] = mapStringMap(deployment.Labels)
		}
		if request.IncludeContainers {
			entry["containers"] = buildDeploymentContainerOutput(deployment)
		}
		deployments = append(deployments, entry)
	}

	summary := fmt.Sprintf("listed %d deployments from namespace %s", len(deployments), namespace)
	output := map[string]any{
		"namespace":          namespace,
		"label_selector":     strings.TrimSpace(request.LabelSelector),
		"field_selector":     strings.TrimSpace(request.FieldSelector),
		"source":             "k8s_sdk",
		"count":              len(deployments),
		"truncated":          truncated,
		"include_containers": request.IncludeContainers,
		"include_labels":     request.IncludeLabels,
		"deployments":        deployments,
		"summary":            summary,
		"output":             summary,
		"exit_code":          0,
	}
	return k8sResult(output, "k8s-get-deployments-report.json"), nil
}

func (h *K8sGetImagesHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace        string `json:"namespace"`
		LabelSelector    string `json:"label_selector"`
		FieldSelector    string `json:"field_selector"`
		MaxImages        int    `json:"max_images"`
		IncludeWorkloads bool   `json:"include_workloads"`
	}{
		MaxImages: 200,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	list, runErr := h.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.TrimSpace(request.LabelSelector),
		FieldSelector: strings.TrimSpace(request.FieldSelector),
	})
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("k8s get images failed: %v", runErr),
			Retryable: true,
		}
	}

	imageIndex := buildImageIndex(list.Items)

	images := make([]*k8sImageUsage, 0, len(imageIndex))
	for _, usage := range imageIndex {
		images = append(images, usage)
	}
	sort.Slice(images, func(i, j int) bool {
		if images[i].Occurrences != images[j].Occurrences {
			return images[i].Occurrences > images[j].Occurrences
		}
		return images[i].Image < images[j].Image
	})

	maxImages := clampInt(request.MaxImages, 1, 1000, 200)
	truncated := false
	if len(images) > maxImages {
		images = images[:maxImages]
		truncated = true
	}

	imageOutputs := buildImageOutputs(images, request.IncludeWorkloads)

	summary := fmt.Sprintf("listed %d images from namespace %s", len(imageOutputs), namespace)
	output := map[string]any{
		"namespace":         namespace,
		"label_selector":    strings.TrimSpace(request.LabelSelector),
		"field_selector":    strings.TrimSpace(request.FieldSelector),
		"source":            "k8s_sdk",
		"count":             len(imageOutputs),
		"truncated":         truncated,
		"include_workloads": request.IncludeWorkloads,
		"images":            imageOutputs,
		"summary":           summary,
		"output":            summary,
		"exit_code":         0,
	}
	return k8sResult(output, "k8s-get-images-report.json"), nil
}

type k8sImageUsage struct {
	Image       string
	Occurrences int
	Pods        map[string]struct{}
	Workloads   map[string]struct{}
}

func buildImageIndex(pods []corev1.Pod) map[string]*k8sImageUsage {
	index := map[string]*k8sImageUsage{}
	for _, pod := range pods {
		for _, container := range pod.Spec.InitContainers {
			indexImageUsage(index, pod.Name, container.Name, container.Image)
		}
		for _, container := range pod.Spec.Containers {
			indexImageUsage(index, pod.Name, container.Name, container.Image)
		}
	}
	return index
}

func indexImageUsage(index map[string]*k8sImageUsage, podName, containerName, image string) {
	candidate := strings.TrimSpace(image)
	if candidate == "" {
		return
	}
	entry := index[candidate]
	if entry == nil {
		entry = &k8sImageUsage{
			Image:     candidate,
			Pods:      map[string]struct{}{},
			Workloads: map[string]struct{}{},
		}
		index[candidate] = entry
	}
	entry.Occurrences++
	entry.Pods[podName] = struct{}{}
	entry.Workloads[podName+"/"+containerName] = struct{}{}
}

func buildImageOutputs(images []*k8sImageUsage, includeWorkloads bool) []map[string]any {
	outputs := make([]map[string]any, 0, len(images))
	for _, usage := range images {
		entry := map[string]any{
			"image":       usage.Image,
			"occurrences": usage.Occurrences,
			"pod_count":   len(usage.Pods),
		}
		if includeWorkloads {
			entry["pods"] = sortedStringSet(usage.Pods)
			entry["workloads"] = sortedStringSet(usage.Workloads)
		}
		outputs = append(outputs, entry)
	}
	return outputs
}

func (h *K8sGetLogsHandler) Invoke(ctx context.Context, session domain.Session, args json.RawMessage) (app.ToolRunResult, *domain.Error) {
	request := struct {
		Namespace    string `json:"namespace"`
		PodName      string `json:"pod_name"`
		Container    string `json:"container"`
		TailLines    int64  `json:"tail_lines"`
		SinceSeconds int64  `json:"since_seconds"`
		Previous     bool   `json:"previous"`
		MaxBytes     int    `json:"max_bytes"`
	}{
		TailLines:    200,
		SinceSeconds: 0,
		MaxBytes:     256 * 1024,
	}
	if err := decodeK8sArgs(args, &request); err != nil {
		return app.ToolRunResult{}, err
	}
	if err := ensureK8sClient(h.client); err != nil {
		return app.ToolRunResult{}, err
	}
	podName := strings.TrimSpace(request.PodName)
	if podName == "" {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "pod_name is required",
			Retryable: false,
		}
	}

	namespace := resolveK8sNamespace(request.Namespace, session, h.defaultNamespace)
	maxBytes := clampInt(request.MaxBytes, 1024, 2*1024*1024, 256*1024)
	tailLines := request.TailLines
	if tailLines <= 0 {
		tailLines = 200
	}
	if tailLines > 10000 {
		tailLines = 10000
	}
	sinceSeconds := request.SinceSeconds
	if sinceSeconds < 0 {
		sinceSeconds = 0
	}

	logOptions := &corev1.PodLogOptions{
		Container: strings.TrimSpace(request.Container),
		TailLines: int64Ptr(tailLines),
		Previous:  request.Previous,
	}
	if sinceSeconds > 0 {
		logOptions.SinceSeconds = int64Ptr(sinceSeconds)
	}

	rawLogs, runErr := h.client.CoreV1().Pods(namespace).GetLogs(podName, logOptions).DoRaw(ctx)
	if runErr != nil {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("k8s get logs failed: %v", runErr),
			Retryable: true,
		}
	}

	truncated := false
	if len(rawLogs) > maxBytes {
		truncated = true
	}
	trimmed := truncate(rawLogs, maxBytes)
	logText := string(trimmed)
	lineCount := 0
	if strings.TrimSpace(logText) != "" {
		lineCount = strings.Count(logText, "\n") + 1
	}

	summary := fmt.Sprintf("retrieved logs for pod %s/%s", namespace, podName)
	output := map[string]any{
		"namespace":     namespace,
		"pod_name":      podName,
		"container":     strings.TrimSpace(request.Container),
		"tail_lines":    tailLines,
		"since_seconds": sinceSeconds,
		"previous":      request.Previous,
		"source":        "k8s_sdk",
		"bytes":         len(trimmed),
		"line_count":    lineCount,
		"truncated":     truncated,
		"logs":          logText,
		"summary":       summary,
		"output":        summary,
		"exit_code":     0,
	}

	reportBytes, marshalErr := json.MarshalIndent(output, "", "  ")
	artifacts := []app.ArtifactPayload{
		{
			Name:        "k8s-get-logs-report.json",
			ContentType: "application/json",
			Data:        reportBytes,
		},
		{
			Name:        "k8s-get-logs.txt",
			ContentType: "text/plain",
			Data:        trimmed,
		},
	}
	if marshalErr != nil {
		artifacts = artifacts[1:]
	}
	return app.ToolRunResult{
		ExitCode:  0,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: summary}},
		Output:    output,
		Artifacts: artifacts,
	}, nil
}

func decodeK8sArgs(args json.RawMessage, destination any) *domain.Error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, destination); err != nil {
		return &domain.Error{
			Code:      app.ErrorCodeInvalidArgument,
			Message:   "invalid k8s tool args",
			Retryable: false,
		}
	}
	return nil
}

func ensureK8sClient(client kubernetes.Interface) *domain.Error {
	if client != nil {
		return nil
	}
	return &domain.Error{
		Code:      app.ErrorCodeExecutionFailed,
		Message:   "kubernetes client is not configured",
		Retryable: false,
	}
}

func resolveK8sNamespace(requestNamespace string, session domain.Session, fallback string) string {
	namespace := strings.TrimSpace(requestNamespace)
	if namespace == "" {
		namespace = strings.TrimSpace(session.Runtime.Namespace)
	}
	if namespace == "" {
		namespace = strings.TrimSpace(fallback)
	}
	if namespace == "" {
		namespace = "default"
	}
	return namespace
}

func truncatePods(items []corev1.Pod, max int) ([]corev1.Pod, bool) {
	if len(items) <= max {
		return items, false
	}
	return items[:max], true
}

func truncateServices(items []corev1.Service, max int) ([]corev1.Service, bool) {
	if len(items) <= max {
		return items, false
	}
	return items[:max], true
}

func truncateDeployments(items []appsv1.Deployment, max int) ([]appsv1.Deployment, bool) {
	if len(items) <= max {
		return items, false
	}
	return items[:max], true
}

func k8sResult(output map[string]any, artifactName string) app.ToolRunResult {
	summary := asString(output["summary"])
	reportBytes, marshalErr := json.MarshalIndent(output, "", "  ")
	artifacts := []app.ArtifactPayload{
		{
			Name:        artifactName,
			ContentType: "application/json",
			Data:        reportBytes,
		},
	}
	if marshalErr != nil {
		artifacts = []app.ArtifactPayload{}
	}

	return app.ToolRunResult{
		ExitCode:  0,
		Logs:      []domain.LogLine{{At: time.Now().UTC(), Channel: "stdout", Message: summary}},
		Output:    output,
		Artifacts: artifacts,
	}
}

func buildPodContainerOutput(pod corev1.Pod) []map[string]any {
	containerNames := make([]string, 0, len(pod.Spec.Containers))
	containerImages := make(map[string]string, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		containerNames = append(containerNames, container.Name)
		containerImages[container.Name] = container.Image
	}
	sort.Strings(containerNames)

	statusByContainer := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		statusByContainer[status.Name] = status
	}

	out := make([]map[string]any, 0, len(containerNames))
	for _, name := range containerNames {
		status, hasStatus := statusByContainer[name]
		state := "unknown"
		if hasStatus {
			switch {
			case status.State.Running != nil:
				state = "running"
			case status.State.Waiting != nil:
				state = "waiting"
			case status.State.Terminated != nil:
				state = "terminated"
			}
		}

		restartCount := int32(0)
		ready := false
		if hasStatus {
			restartCount = status.RestartCount
			ready = status.Ready
		}

		out = append(out, map[string]any{
			"name":          name,
			"image":         containerImages[name],
			"ready":         ready,
			"restart_count": restartCount,
			"state":         state,
		})
	}
	return out
}

func buildDeploymentContainerOutput(deployment appsv1.Deployment) []map[string]any {
	out := make([]map[string]any, 0, len(deployment.Spec.Template.Spec.Containers))
	for _, container := range deployment.Spec.Template.Spec.Containers {
		out = append(out, map[string]any{
			"name":  container.Name,
			"image": container.Image,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return asString(out[i]["name"]) < asString(out[j]["name"])
	})
	return out
}

func sortedStringSet(input map[string]struct{}) []string {
	out := make([]string, 0, len(input))
	for value := range input {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func mapStringMap(input map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range input {
		out[key] = value
	}
	return out
}

func derefInt32(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
