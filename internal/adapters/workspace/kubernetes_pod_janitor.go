package workspace

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultK8sPodJanitorInterval        = 60 * time.Second
	defaultK8sSessionTerminalPodTTL     = 5 * time.Minute
	defaultK8sContainerTerminalPodTTL   = 5 * time.Minute
	defaultK8sMissingSessionGracePeriod = 2 * time.Minute
)

type KubernetesPodJanitorConfig struct {
	Namespace                 string
	SessionStore              app.SessionStore
	Interval                  time.Duration
	SessionTerminalPodTTL     time.Duration
	ContainerTerminalPodTTL   time.Duration
	MissingSessionGracePeriod time.Duration
	Logger                    *slog.Logger
}

type KubernetesPodJanitor struct {
	client kubernetes.Interface
	cfg    KubernetesPodJanitorConfig
	logger *slog.Logger
}

func NewKubernetesPodJanitor(client kubernetes.Interface, cfg KubernetesPodJanitorConfig) *KubernetesPodJanitor {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultK8sPodJanitorInterval
	}
	if cfg.SessionTerminalPodTTL <= 0 {
		cfg.SessionTerminalPodTTL = defaultK8sSessionTerminalPodTTL
	}
	if cfg.ContainerTerminalPodTTL <= 0 {
		cfg.ContainerTerminalPodTTL = defaultK8sContainerTerminalPodTTL
	}
	if cfg.MissingSessionGracePeriod <= 0 {
		cfg.MissingSessionGracePeriod = defaultK8sMissingSessionGracePeriod
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &KubernetesPodJanitor{
		client: client,
		cfg:    cfg,
		logger: logger,
	}
}

func (j *KubernetesPodJanitor) Start(ctx context.Context) {
	if j == nil || j.client == nil || strings.TrimSpace(j.cfg.Namespace) == "" {
		return
	}
	j.Sweep(ctx)

	ticker := time.NewTicker(j.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.Sweep(ctx)
		}
	}
}

func (j *KubernetesPodJanitor) Sweep(ctx context.Context) {
	if j == nil || j.client == nil || strings.TrimSpace(j.cfg.Namespace) == "" {
		return
	}
	now := time.Now().UTC()
	j.sweepSessionPods(ctx, now)
	j.sweepContainerPods(ctx, now)
}

func (j *KubernetesPodJanitor) sweepSessionPods(ctx context.Context, now time.Time) {
	list, err := j.client.CoreV1().Pods(j.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=workspace-session",
	})
	if err != nil {
		j.logger.Warn("pod janitor: list workspace session pods failed", "error", err)
		return
	}

	for _, pod := range list.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		reason := j.resolveSessionPodDeleteReason(ctx, pod, now)
		if reason == "" {
			continue
		}
		j.deletePod(ctx, pod, reason)
	}
}

func (j *KubernetesPodJanitor) sweepContainerPods(ctx context.Context, now time.Time) {
	list, err := j.client.CoreV1().Pods(j.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=workspace-container-run",
	})
	if err != nil {
		j.logger.Warn("pod janitor: list workspace container pods failed", "error", err)
		return
	}

	for _, pod := range list.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		reason := j.resolveContainerPodDeleteReason(ctx, pod, now)
		if reason == "" {
			continue
		}
		j.deletePod(ctx, pod, reason)
	}
}

func (j *KubernetesPodJanitor) resolveSessionPodDeleteReason(ctx context.Context, pod corev1.Pod, now time.Time) string {
	age := now.Sub(pod.CreationTimestamp.Time)
	if isTerminalOrUnknownPhase(pod.Status.Phase) && age >= j.cfg.SessionTerminalPodTTL {
		return "terminal_pod_ttl_expired"
	}

	if j.cfg.SessionStore == nil {
		return ""
	}
	sessionID := strings.TrimSpace(pod.Labels["workspace_id"])
	if sessionID == "" || sessionID == "unknown" {
		return ""
	}

	session, found, err := j.cfg.SessionStore.Get(ctx, sessionID)
	if err != nil {
		j.logger.Warn(
			"pod janitor: load workspace session failed",
			"session_id", sessionID,
			"pod", pod.Name,
			"error", err,
		)
		return ""
	}
	if !found {
		if age >= j.cfg.MissingSessionGracePeriod {
			return "session_not_found"
		}
		return ""
	}

	if !session.ExpiresAt.IsZero() && now.After(session.ExpiresAt) {
		if err := j.cfg.SessionStore.Delete(ctx, sessionID); err != nil {
			j.logger.Warn("pod janitor: delete expired session key failed", "session_id", sessionID, "error", err)
		}
		return "session_expired"
	}
	return ""
}

func (j *KubernetesPodJanitor) resolveContainerPodDeleteReason(ctx context.Context, pod corev1.Pod, now time.Time) string {
	age := now.Sub(pod.CreationTimestamp.Time)
	if isTerminalOrUnknownPhase(pod.Status.Phase) && age >= j.cfg.ContainerTerminalPodTTL {
		return "terminal_pod_ttl_expired"
	}

	if j.cfg.SessionStore == nil {
		return ""
	}
	sessionID := strings.TrimSpace(pod.Labels["workspace_session_id"])
	if sessionID == "" || sessionID == "unknown" {
		return ""
	}

	session, found, err := j.cfg.SessionStore.Get(ctx, sessionID)
	if err != nil {
		j.logger.Warn(
			"pod janitor: load container session failed",
			"session_id", sessionID,
			"pod", pod.Name,
			"error", err,
		)
		return ""
	}
	if !found {
		if age >= j.cfg.MissingSessionGracePeriod {
			return "session_not_found"
		}
		return ""
	}
	if !session.ExpiresAt.IsZero() && now.After(session.ExpiresAt) {
		return "session_expired"
	}
	return ""
}

func (j *KubernetesPodJanitor) deletePod(ctx context.Context, pod corev1.Pod, reason string) {
	if err := j.client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		j.logger.Warn("pod janitor: delete pod failed", "namespace", pod.Namespace, "pod", pod.Name, "reason", reason, "error", err)
		return
	}
	j.logger.Info(
		"pod janitor: deleted pod",
		"namespace", pod.Namespace,
		"pod", pod.Name,
		"phase", strings.ToLower(strings.TrimSpace(string(pod.Status.Phase))),
		"reason", reason,
	)
}

func isTerminalOrUnknownPhase(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed || phase == corev1.PodUnknown
}
