//go:build k8s

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// containerK8sAdapter implements containerK8sOps using a real Kubernetes client.
type containerK8sAdapter struct {
	client           kubernetes.Interface
	defaultNamespace string
	runner           app.CommandRunner
}

func (a *containerK8sAdapter) invokePS(ctx context.Context, session domain.Session, all bool, limit int, nameFilter string) (app.ToolRunResult, *domain.Error) {
	if err := ensureK8sClient(a.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace("", session, a.defaultNamespace)
	selectors := []string{"app=workspace-container-run"}
	if sessionID := strings.TrimSpace(session.ID); sessionID != "" {
		selectors = append(selectors, "workspace_session_id="+sanitizeContainerLabelValue(sessionID))
	}

	podList, err := a.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: strings.Join(selectors, ","),
	})
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s container ps failed: %v", err), true)
	}

	filter := strings.ToLower(strings.TrimSpace(nameFilter))
	containers := make([]map[string]any, 0, len(podList.Items))
	for i := range podList.Items {
		pod := &podList.Items[i]
		if !all && pod.Status.Phase != corev1.PodRunning {
			continue
		}

		podName := strings.TrimSpace(pod.Name)
		if podName == "" {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(podName), filter) {
			continue
		}

		image := resolveK8sPodImage(pod)

		status := strings.ToLower(strings.TrimSpace(string(pod.Status.Phase)))
		if status == "" {
			status = "unknown"
		}
		containers = append(containers, map[string]any{
			"id":     podName,
			"image":  image,
			"name":   podName,
			"status": status,
		})
	}

	sort.Slice(containers, func(i, j int) bool {
		return asString(containers[i]["id"]) < asString(containers[j]["id"])
	})

	truncated := false
	if len(containers) > limit {
		containers = containers[:limit]
		truncated = true
	}

	summary := fmt.Sprintf("listed %d containers", len(containers))
	output := map[string]any{
		containerSourceRuntime:   "k8s",
		containerSourceSimulated: false,
		"all":                    all,
		"limit":                  limit,
		containerKeyNameFilter:   nameFilter,
		"count":                  len(containers),
		containerKeyTruncated:    truncated,
		"containers":             containers,
		containerKeyNamespace:    namespace,
		containerKeySummary:      summary,
		containerKeyOutput:       summary,
		containerKeyExitCode:     0,
	}
	return containerResult(output, summary, containerPsReportJSON, containerPsOutputTxt), nil
}

func (a *containerK8sAdapter) invokeLogs(ctx context.Context, session domain.Session, containerID string, tailLines, sinceSec int, timestamps bool, maxBytes int) (app.ToolRunResult, *domain.Error) {
	if err := ensureK8sClient(a.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace("", session, a.defaultNamespace)
	pod, err := a.client.CoreV1().Pods(namespace).Get(ctx, containerID, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeNotFound,
			Message:   "container pod not found",
			Retryable: false,
		}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf(containerFmtK8sLogsFailed, err), true)
	}

	containerName := resolveK8sRunContainerName(pod)
	options := &corev1.PodLogOptions{
		Container:  containerName,
		Timestamps: timestamps,
	}
	tailLines64 := int64(tailLines)
	options.TailLines = &tailLines64
	if sinceSec > 0 {
		sinceSec64 := int64(sinceSec)
		options.SinceSeconds = &sinceSec64
	}

	stream, streamErr := a.client.CoreV1().Pods(namespace).GetLogs(containerID, options).Stream(ctx)
	if streamErr != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf(containerFmtK8sLogsFailed, streamErr), true)
	}
	defer func() { _ = stream.Close() }()

	rawLogs, readErr := io.ReadAll(stream)
	if readErr != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf(containerFmtK8sLogsFailed, readErr), true)
	}

	logText := string(rawLogs)
	output := buildContainerLogsOutput(containerLogsParams{runtime: "k8s", simulated: false, containerID: containerID, tailLines: tailLines, sinceSec: sinceSec, timestamps: timestamps, raw: logText, maxBytes: maxBytes, exitCode: 0})
	output[containerKeyNamespace] = namespace
	output["pod_name"] = containerID
	output["container"] = containerName
	output["source"] = "k8s_sdk"
	return containerResult(output, logText, containerLogsReportJSON, containerLogsOutputTxt), nil
}

func (a *containerK8sAdapter) invokeRun(ctx context.Context, session domain.Session, p k8sRunParams) (app.ToolRunResult, *domain.Error) {
	imageRef := p.imageRef
	command := p.command
	envPairs := p.envPairs
	containerName := p.containerName
	detach := p.detach
	remove := p.remove
	if err := ensureK8sClient(a.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace("", session, a.defaultNamespace)
	labels := buildK8sPodLabels(session)
	podName := buildK8sRunPodName(session.ID, containerName, imageRef, command)

	runContainer := buildK8sRunContainer(imageRef, command, envPairs)

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers:    []corev1.Container{runContainer},
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser:    int64Ptr(1000),
			RunAsGroup:   int64Ptr(1000),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		AutomountServiceAccountToken: boolPtr(false),
	}

	podTemplate := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: podSpec,
	}
	pod, err := a.client.CoreV1().Pods(namespace).Create(ctx, podTemplate, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		retryName := fmt.Sprintf("%s-%d", strings.TrimSuffix(podName, "-"), time.Now().UnixNano()%1000)
		if len(retryName) > 63 {
			retryName = strings.TrimSuffix(retryName[:63], "-")
		}
		podTemplate.Name = retryName
		pod, err = a.client.CoreV1().Pods(namespace).Create(ctx, podTemplate, metav1.CreateOptions{})
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s container run failed: %v", err), true)
	}

	startedPod, startErr := waitForK8sContainerPodStarted(ctx, a.client, namespace, pod.Name, 30*time.Second)
	if startErr != nil {
		return app.ToolRunResult{}, startErr
	}

	exitCode := 0
	status := strings.ToLower(strings.TrimSpace(string(startedPod.Status.Phase)))
	if status == "" {
		status = "pending"
	}

	status, exitCode, waitErr := waitForK8sPodCompletion(ctx, a.client, podCompletionConfig{
		namespace: namespace, podName: pod.Name,
		detach: detach, remove: remove,
		status: status, exitCode: exitCode,
	})
	if waitErr != nil {
		return app.ToolRunResult{}, waitErr
	}

	summary := fmt.Sprintf("k8s container pod started: %s/%s", namespace, pod.Name)
	output := map[string]any{
		containerSourceRuntime:   "k8s",
		containerSourceSimulated: false,
		containerKeyNamespace:    namespace,
		"pod_name":               pod.Name,
		"image_ref":              imageRef,
		"name":                   containerName,
		"detach":                 detach,
		"remove":                 remove,
		containerKeyCommand:      command,
		"env":                    envPairs,
		containerKeyContainerID:  pod.Name,
		containerKeyStatus:       status,
		containerKeySummary:      summary,
		containerKeyOutput:       summary,
		containerKeyExitCode:     exitCode,
	}
	return containerResult(output, summary, containerRunReportJSON, containerRunOutputTxt), nil
}

func (a *containerK8sAdapter) invokeExec(ctx context.Context, session domain.Session, containerID string, command []string, timeoutSec, maxBytes int) (app.ToolRunResult, *domain.Error) {
	if err := ensureK8sClient(a.client); err != nil {
		return app.ToolRunResult{}, err
	}

	namespace := resolveK8sNamespace("", session, a.defaultNamespace)
	pod, err := a.client.CoreV1().Pods(namespace).Get(ctx, containerID, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeNotFound,
			Message:   "container pod not found",
			Retryable: false,
		}
	}
	if err != nil {
		return app.ToolRunResult{}, k8sExecutionFailed(fmt.Sprintf("k8s container exec failed: %v", err), true)
	}
	if pod.Status.Phase != corev1.PodRunning {
		return app.ToolRunResult{}, &domain.Error{
			Code:      app.ErrorCodeExecutionFailed,
			Message:   fmt.Sprintf("container pod is not running (phase=%s)", strings.ToLower(string(pod.Status.Phase))),
			Retryable: false,
		}
	}

	containerName := resolveK8sRunContainerName(pod)
	runner := ensureRunner(a.runner)
	execSession := session
	execSession.Runtime.Kind = domain.RuntimeKindKubernetes
	execSession.Runtime.Namespace = namespace
	execSession.Runtime.PodName = containerID
	execSession.Runtime.Container = containerName
	execSession.Runtime.Workdir = "/"

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	commandResult, runErr := runner.Run(timeoutCtx, execSession, app.CommandSpec{
		Cwd:      "",
		Command:  command[0],
		Args:     command[1:],
		MaxBytes: maxBytes,
	})
	if runErr != nil {
		result := containerResult(
			map[string]any{
				containerSourceRuntime:     "k8s",
				containerSourceSimulated:   false,
				containerKeyNamespace:      namespace,
				"pod_name":                 containerID,
				"container":                containerName,
				containerKeyContainerID:    containerID,
				containerKeyCommand:        command,
				containerKeyTimeoutSeconds: timeoutSec,
				containerKeyExitCode:       commandResult.ExitCode,
				containerKeySummary:        "container exec failed",
				containerKeyOutput:         strings.TrimSpace(commandResult.Output),
			},
			commandResult.Output,
			containerExecReportJSON,
			containerExecOutputTxt,
		)
		return result, toToolError(runErr, commandResult.Output)
	}

	output := map[string]any{
		containerSourceRuntime:     "k8s",
		containerSourceSimulated:   false,
		containerKeyNamespace:      namespace,
		"pod_name":                 containerID,
		"container":                containerName,
		containerKeyContainerID:    containerID,
		containerKeyCommand:        command,
		containerKeyTimeoutSeconds: timeoutSec,
		containerKeyExitCode:       commandResult.ExitCode,
		containerKeySummary:        "container exec completed",
		containerKeyOutput:         strings.TrimSpace(commandResult.Output),
	}
	return containerResult(output, commandResult.Output, containerExecReportJSON, containerExecOutputTxt), nil
}

// --- K8s helper functions ---

func resolveK8sPodImage(pod *corev1.Pod) string {
	containerName := resolveK8sRunContainerName(pod)
	for i := range pod.Spec.Containers {
		if strings.TrimSpace(pod.Spec.Containers[i].Name) == containerName {
			return strings.TrimSpace(pod.Spec.Containers[i].Image)
		}
	}
	if len(pod.Spec.Containers) > 0 {
		return strings.TrimSpace(pod.Spec.Containers[0].Image)
	}
	return ""
}

func buildK8sPodLabels(session domain.Session) map[string]string {
	labels := map[string]string{
		"app":                  "workspace-container-run",
		"workspace_session_id": sanitizeContainerLabelValue(session.ID),
	}
	if tenant := strings.TrimSpace(session.Principal.TenantID); tenant != "" {
		labels["workspace_tenant"] = sanitizeContainerLabelValue(tenant)
	}
	return labels
}

func buildK8sRunContainer(imageRef string, command, envPairs []string) corev1.Container {
	environment := make([]corev1.EnvVar, 0, len(envPairs))
	for _, pair := range envPairs {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		environment = append(environment, corev1.EnvVar{Name: key, Value: value})
	}
	c := corev1.Container{
		Name:            "task",
		Image:           imageRef,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             environment,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: boolPtr(false),
			ReadOnlyRootFilesystem:   boolPtr(false),
			RunAsNonRoot:             boolPtr(true),
			RunAsUser:                int64Ptr(1000),
			RunAsGroup:               int64Ptr(1000),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	}
	if len(command) > 0 {
		c.Command = append([]string{}, command...)
	}
	return c
}

type podCompletionConfig struct {
	namespace string
	podName   string
	detach    bool
	remove    bool
	status    string
	exitCode  int
}

func waitForK8sPodCompletion(ctx context.Context, client kubernetes.Interface, cfg podCompletionConfig) (string, int, *domain.Error) {
	if cfg.detach {
		return cfg.status, cfg.exitCode, nil
	}
	terminalPod, terminalErr := waitForK8sContainerPodTerminal(ctx, client, cfg.namespace, cfg.podName, 120*time.Second)
	if terminalErr != nil {
		return cfg.status, cfg.exitCode, terminalErr
	}
	status := strings.ToLower(strings.TrimSpace(string(terminalPod.Status.Phase)))
	exitCode := cfg.exitCode
	if terminated, ok := firstTerminatedContainerStatus(terminalPod.Status); ok {
		exitCode = int(terminated.ExitCode)
	}
	if cfg.remove {
		_ = client.CoreV1().Pods(cfg.namespace).Delete(ctx, cfg.podName, metav1.DeleteOptions{})
	}
	return status, exitCode, nil
}

func buildK8sRunPodName(sessionID, containerName, imageRef string, command []string) string {
	base := "ws-ctr-" + sanitizeContainerLabelValue(sessionID)
	if strings.TrimSpace(containerName) != "" {
		namePart := strings.ToLower(strings.TrimSpace(containerName))
		namePart = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return '-'
		}, namePart)
		namePart = strings.Trim(namePart, "-")
		if namePart != "" {
			base = "ws-ctr-" + namePart
		}
	}
	if len(base) > 55 {
		base = strings.TrimSuffix(base[:55], "-")
	}
	if base == "" {
		base = "ws-ctr"
	}
	suffixSeed := sessionID + "|" + imageRef + "|" + strings.Join(command, " ") + "|" + containerName
	suffixHash := sha256.Sum256([]byte(suffixSeed))
	suffix := hex.EncodeToString(suffixHash[:])
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}
	return strings.TrimSuffix(base, "-") + "-" + suffix
}

func waitForK8sContainerPodStarted(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	podName string,
	timeout time.Duration,
) (*corev1.Pod, *domain.Error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		pod, err := client.CoreV1().Pods(namespace).Get(deadlineCtx, podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			select {
			case <-deadlineCtx.Done():
				return nil, k8sExecutionFailed("k8s container pod did not become ready before timeout", false)
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			return nil, k8sExecutionFailed(fmt.Sprintf("k8s container run failed: %v", err), true)
		}
		switch pod.Status.Phase {
		case corev1.PodRunning, corev1.PodSucceeded:
			return pod, nil
		case corev1.PodFailed:
			return nil, k8sExecutionFailed("k8s container pod failed to start", false)
		}

		select {
		case <-deadlineCtx.Done():
			return nil, k8sExecutionFailed("k8s container pod did not become ready before timeout", false)
		case <-ticker.C:
		}
	}
}

func waitForK8sContainerPodTerminal(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	podName string,
	timeout time.Duration,
) (*corev1.Pod, *domain.Error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		pod, err := client.CoreV1().Pods(namespace).Get(deadlineCtx, podName, metav1.GetOptions{})
		if err != nil {
			return nil, k8sExecutionFailed(fmt.Sprintf("k8s container wait failed: %v", err), true)
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			return pod, nil
		}
		select {
		case <-deadlineCtx.Done():
			return nil, k8sExecutionFailed("k8s container pod did not complete before timeout", false)
		case <-ticker.C:
		}
	}
}

func resolveK8sRunContainerName(pod *corev1.Pod) string {
	if pod == nil {
		return "task"
	}
	for i := range pod.Spec.Containers {
		if strings.TrimSpace(pod.Spec.Containers[i].Name) == "task" {
			return "task"
		}
	}
	if len(pod.Spec.Containers) > 0 {
		name := strings.TrimSpace(pod.Spec.Containers[0].Name)
		if name != "" {
			return name
		}
	}
	return "task"
}

func firstTerminatedContainerStatus(status corev1.PodStatus) (corev1.ContainerStateTerminated, bool) {
	for i := range status.ContainerStatuses {
		if status.ContainerStatuses[i].State.Terminated != nil {
			return *status.ContainerStatuses[i].State.Terminated, true
		}
	}
	return corev1.ContainerStateTerminated{}, false
}

// --- WithKubernetes constructors ---

func NewContainerPSHandlerWithKubernetes(runner app.CommandRunner, client kubernetes.Interface, namespace string) *ContainerPSHandler {
	return &ContainerPSHandler{
		runner:           runner,
		k8sOps:           &containerK8sAdapter{client: client, defaultNamespace: strings.TrimSpace(namespace), runner: runner},
		defaultNamespace: strings.TrimSpace(namespace),
	}
}

func NewContainerLogsHandlerWithKubernetes(runner app.CommandRunner, client kubernetes.Interface, namespace string) *ContainerLogsHandler {
	return &ContainerLogsHandler{
		runner:           runner,
		k8sOps:           &containerK8sAdapter{client: client, defaultNamespace: strings.TrimSpace(namespace), runner: runner},
		defaultNamespace: strings.TrimSpace(namespace),
	}
}

func NewContainerRunHandlerWithKubernetes(runner app.CommandRunner, client kubernetes.Interface, namespace string) *ContainerRunHandler {
	return &ContainerRunHandler{
		runner:           runner,
		k8sOps:           &containerK8sAdapter{client: client, defaultNamespace: strings.TrimSpace(namespace), runner: runner},
		defaultNamespace: strings.TrimSpace(namespace),
	}
}

func NewContainerExecHandlerWithKubernetes(runner app.CommandRunner, client kubernetes.Interface, namespace string) *ContainerExecHandler {
	return &ContainerExecHandler{
		runner:           runner,
		k8sOps:           &containerK8sAdapter{client: client, defaultNamespace: strings.TrimSpace(namespace), runner: runner},
		defaultNamespace: strings.TrimSpace(namespace),
	}
}
