package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

const (
	defaultK8sNamespace     = "underpass-runtime"
	defaultK8sRunnerImage   = "docker.io/library/alpine:3.20"
	defaultK8sInitImage     = "docker.io/alpine/git:2.45.2"
	defaultK8sRunnerProfile = "runner_profile"
	defaultK8sWorkspaceDir  = "/workspace/repo"
	defaultK8sContainerName = "runner"
	defaultK8sPodNamePrefix = "ws"
	defaultK8sReadyTimeout  = 120 * time.Second
	defaultK8sRunAsUser     = int64(1000)
	defaultK8sRunAsGroup    = int64(1000)
	defaultK8sFSGroup       = int64(1000)
	defaultGitAuthMetaKey   = "git_auth_secret"
	gitAuthMountPath        = "/var/run/workspace-git-auth"
	shellIfF                = "if [ -f "
	shellThen               = " ]; then "
)

type KubernetesManagerConfig struct {
	Namespace           string
	ServiceAccount      string
	PodImage            string
	RunnerImageBundles  map[string]string
	RunnerProfileKey    string
	InitImage           string
	WorkspaceDir        string
	RunnerContainerName string
	PodNamePrefix       string
	PodReadyTimeout     time.Duration
	SessionStore        app.SessionStore
	GitAuthSecretName   string
	GitAuthMetadataKey  string
	RunAsUser           int64
	RunAsGroup          int64
	FSGroup             int64
	ReadOnlyRootFS      bool
	AutomountSAToken    bool
}

type KubernetesManager struct {
	cfg    KubernetesManagerConfig
	client kubernetes.Interface
	store  app.SessionStore
}

func NewKubernetesManager(cfg KubernetesManagerConfig, client kubernetes.Interface) *KubernetesManager {
	resolved := cfg
	if strings.TrimSpace(resolved.Namespace) == "" {
		resolved.Namespace = defaultK8sNamespace
	}
	if strings.TrimSpace(resolved.PodImage) == "" {
		resolved.PodImage = defaultK8sRunnerImage
	}
	resolved.RunnerImageBundles = normalizeRunnerImageBundles(resolved.RunnerImageBundles)
	if strings.TrimSpace(resolved.RunnerProfileKey) == "" {
		resolved.RunnerProfileKey = defaultK8sRunnerProfile
	}
	if strings.TrimSpace(resolved.InitImage) == "" {
		resolved.InitImage = defaultK8sInitImage
	}
	if strings.TrimSpace(resolved.WorkspaceDir) == "" {
		resolved.WorkspaceDir = defaultK8sWorkspaceDir
	}
	if strings.TrimSpace(resolved.RunnerContainerName) == "" {
		resolved.RunnerContainerName = defaultK8sContainerName
	}
	if strings.TrimSpace(resolved.PodNamePrefix) == "" {
		resolved.PodNamePrefix = defaultK8sPodNamePrefix
	}
	if resolved.PodReadyTimeout <= 0 {
		resolved.PodReadyTimeout = defaultK8sReadyTimeout
	}
	if strings.TrimSpace(resolved.GitAuthMetadataKey) == "" {
		resolved.GitAuthMetadataKey = defaultGitAuthMetaKey
	}
	if resolved.RunAsUser <= 0 {
		resolved.RunAsUser = defaultK8sRunAsUser
	}
	if resolved.RunAsGroup <= 0 {
		resolved.RunAsGroup = defaultK8sRunAsGroup
	}
	if resolved.FSGroup <= 0 {
		resolved.FSGroup = defaultK8sFSGroup
	}
	store := resolved.SessionStore
	if store == nil {
		store = app.NewInMemorySessionStore()
	}

	return &KubernetesManager{
		cfg:    resolved,
		client: client,
		store:  store,
	}
}

func (m *KubernetesManager) CreateSession(ctx context.Context, req app.CreateSessionRequest) (domain.Session, error) {
	if m.client == nil {
		return domain.Session{}, fmt.Errorf("kubernetes client is required")
	}
	if strings.TrimSpace(req.SourceRepoPath) != "" {
		return domain.Session{}, fmt.Errorf("source_repo_path is not supported by kubernetes workspace manager")
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = newK8sSessionID()
	}
	podName := podNameFromSessionID(m.cfg.PodNamePrefix, sessionID)

	allowedPaths := req.AllowedPaths
	if len(allowedPaths) == 0 {
		allowedPaths = []string{"."}
	}

	pod, podErr := m.sessionPod(req, sessionID, podName)
	if podErr != nil {
		return domain.Session{}, podErr
	}
	if _, err := m.client.CoreV1().Pods(m.cfg.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return domain.Session{}, fmt.Errorf("create workspace pod: %w", err)
	}

	if err := m.waitUntilReady(ctx, podName); err != nil {
		_ = m.client.CoreV1().Pods(m.cfg.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})
		return domain.Session{}, err
	}

	now := time.Now().UTC()
	session := domain.Session{
		ID:            sessionID,
		WorkspacePath: m.cfg.WorkspaceDir,
		Runtime: domain.RuntimeRef{
			Kind:      domain.RuntimeKindKubernetes,
			Namespace: m.cfg.Namespace,
			PodName:   podName,
			Container: m.cfg.RunnerContainerName,
			Workdir:   m.cfg.WorkspaceDir,
		},
		RepoURL:      req.RepoURL,
		RepoRef:      req.RepoRef,
		AllowedPaths: allowedPaths,
		Principal:    req.Principal,
		Metadata:     req.Metadata,
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Duration(req.ExpiresInSecond) * time.Second),
	}

	if err := m.store.Save(ctx, session); err != nil {
		_ = m.client.CoreV1().Pods(m.cfg.Namespace).Delete(ctx, podName, metav1.DeleteOptions{})
		return domain.Session{}, fmt.Errorf("persist session: %w", err)
	}
	return session, nil
}

func (m *KubernetesManager) GetSession(ctx context.Context, sessionID string) (domain.Session, bool, error) {
	session, found, err := m.store.Get(ctx, sessionID)
	if err != nil {
		return domain.Session{}, false, fmt.Errorf("load session: %w", err)
	}
	if !found {
		return domain.Session{}, false, nil
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		_ = m.CloseSession(ctx, sessionID)
		return domain.Session{}, false, nil
	}
	return session, true, nil
}

func (m *KubernetesManager) CloseSession(ctx context.Context, sessionID string) error {
	session, found, err := m.store.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	if found {
		if deleteErr := m.store.Delete(ctx, sessionID); deleteErr != nil {
			return fmt.Errorf("delete session: %w", deleteErr)
		}
	}

	namespace := m.cfg.Namespace
	podName := ""
	if found {
		namespace = strings.TrimSpace(session.Runtime.Namespace)
		if namespace == "" {
			namespace = m.cfg.Namespace
		}
		podName = strings.TrimSpace(session.Runtime.PodName)
	}
	if podName == "" {
		discoveredPod, discoveryErr := m.lookupPodNameBySessionID(ctx, namespace, sessionID)
		if discoveryErr != nil {
			return discoveryErr
		}
		if discoveredPod == "" {
			return nil
		}
		podName = discoveredPod
	}

	err = m.client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete workspace pod: %w", err)
	}
	return nil
}

func (m *KubernetesManager) waitUntilReady(ctx context.Context, podName string) error {
	pollErr := wait.PollUntilContextTimeout(
		ctx,
		1500*time.Millisecond,
		m.cfg.PodReadyTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			pod, err := m.client.CoreV1().Pods(m.cfg.Namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodUnknown {
				return false, fmt.Errorf("workspace pod phase=%s", pod.Status.Phase)
			}
			return podReady(pod), nil
		},
	)
	if pollErr != nil {
		return fmt.Errorf("wait workspace pod ready: %w", pollErr)
	}
	return nil
}

func (m *KubernetesManager) sessionPod(req app.CreateSessionRequest, sessionID, podName string) (*corev1.Pod, error) {
	labels := map[string]string{
		"app":              "workspace-session",
		"workspace_id":     sanitizeLabelValue(sessionID),
		"workspace_tenant": sanitizeLabelValue(req.Principal.TenantID),
	}
	workspaceVolumeMount := corev1.VolumeMount{Name: "workspace", MountPath: "/workspace"}
	gitAuthSecretName := m.gitAuthSecretName(req.Metadata)
	hasGitAuth := strings.TrimSpace(gitAuthSecretName) != ""

	volumes := []corev1.Volume{
		{
			Name:         "workspace",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}
	initVolumeMounts := []corev1.VolumeMount{workspaceVolumeMount}
	if hasGitAuth {
		volumes = append(volumes, corev1.Volume{
			Name: "git-auth",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: gitAuthSecretName,
				},
			},
		})
		initVolumeMounts = append(initVolumeMounts, corev1.VolumeMount{
			Name:      "git-auth",
			MountPath: gitAuthMountPath,
			ReadOnly:  true,
		})
	}

	runnerImage, err := m.resolveRunnerImage(req.Metadata)
	if err != nil {
		return nil, err
	}

	spec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Volumes:       volumes,
		InitContainers: []corev1.Container{
			{
				Name:            "repo-init",
				Image:           m.cfg.InitImage,
				ImagePullPolicy: corev1.PullAlways,
				Command: []string{
					"sh",
					"-lc",
					buildRepoInitScript(req.RepoURL, req.RepoRef, m.cfg.WorkspaceDir, hasGitAuth),
				},
				VolumeMounts: initVolumeMounts,
			},
		},
		Containers: []corev1.Container{
			{
				Name:            m.cfg.RunnerContainerName,
				Image:           runnerImage,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"sh", "-lc", "sleep infinity"},
				WorkingDir:      m.cfg.WorkspaceDir,
				VolumeMounts:    []corev1.VolumeMount{workspaceVolumeMount},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: boolPtr(false),
					ReadOnlyRootFilesystem:   boolPtr(m.cfg.ReadOnlyRootFS),
					RunAsNonRoot:             boolPtr(true),
					RunAsUser:                int64Ptr(m.cfg.RunAsUser),
					RunAsGroup:               int64Ptr(m.cfg.RunAsGroup),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: boolPtr(true),
			RunAsUser:    int64Ptr(m.cfg.RunAsUser),
			RunAsGroup:   int64Ptr(m.cfg.RunAsGroup),
			FSGroup:      int64Ptr(m.cfg.FSGroup),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		AutomountServiceAccountToken: boolPtr(m.cfg.AutomountSAToken),
	}

	if sa := strings.TrimSpace(m.cfg.ServiceAccount); sa != "" {
		spec.ServiceAccountName = sa
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: m.cfg.Namespace,
			Labels:    labels,
		},
		Spec: spec,
	}, nil
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func buildRepoInitScript(repoURL, repoRef, workspaceDir string, withGitAuth bool) string {
	commands := []string{
		"set -eu",
		"mkdir -p " + shellQuote(workspaceDir),
		"rm -rf " + shellQuote(workspaceDir) + "/*",
	}
	if withGitAuth {
		commands = append(commands, []string{
			shellIfF + shellQuote(gitAuthMountPath+"/.netrc") + shellThen +
				"cp " + shellQuote(gitAuthMountPath+"/.netrc") + " \"$HOME/.netrc\" && chmod 600 \"$HOME/.netrc\"; " +
				"elif [ -f " + shellQuote(gitAuthMountPath+"/token") + shellThen +
				"GIT_AUTH_USER=$(cat " + shellQuote(gitAuthMountPath+"/username") + " 2>/dev/null || echo oauth2); " +
				"GIT_AUTH_TOKEN=$(tr -d '\\n' < " + shellQuote(gitAuthMountPath+"/token") + "); " +
				"printf 'default login %s password %s\\n' \"$GIT_AUTH_USER\" \"$GIT_AUTH_TOKEN\" > \"$HOME/.netrc\" && chmod 600 \"$HOME/.netrc\"; " +
				"fi",
			shellIfF + shellQuote(gitAuthMountPath+"/ssh-privatekey") + shellThen +
				"mkdir -p \"$HOME/.ssh\" && cp " + shellQuote(gitAuthMountPath+"/ssh-privatekey") + " \"$HOME/.ssh/id_rsa\" && chmod 600 \"$HOME/.ssh/id_rsa\"; " +
				shellIfF + shellQuote(gitAuthMountPath+"/known_hosts") + shellThen +
				"cp " + shellQuote(gitAuthMountPath+"/known_hosts") + " \"$HOME/.ssh/known_hosts\" && chmod 644 \"$HOME/.ssh/known_hosts\"; " +
				"export GIT_SSH_COMMAND='ssh -i $HOME/.ssh/id_rsa -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$HOME/.ssh/known_hosts'; " +
				"else export GIT_SSH_COMMAND='ssh -i $HOME/.ssh/id_rsa -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new'; fi; " +
				"fi",
		}...)
	}
	if strings.TrimSpace(repoURL) != "" {
		clone := []string{"git", "clone", "--depth", "1"}
		if strings.TrimSpace(repoRef) != "" {
			clone = append(clone, "--branch", shellQuote(strings.TrimSpace(repoRef)))
		}
		clone = append(clone, shellQuote(strings.TrimSpace(repoURL)), shellQuote(workspaceDir))
		commands = append(commands, strings.Join(clone, " "))
	}
	return strings.Join(commands, "\n")
}

func (m *KubernetesManager) lookupPodNameBySessionID(ctx context.Context, namespace, sessionID string) (string, error) {
	list, err := m.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "workspace_id=" + sanitizeLabelValue(sessionID),
	})
	if err != nil {
		return "", fmt.Errorf("lookup workspace pod by label: %w", err)
	}
	if len(list.Items) == 0 {
		return "", nil
	}
	return strings.TrimSpace(list.Items[0].Name), nil
}

func (m *KubernetesManager) gitAuthSecretName(metadata map[string]string) string {
	metadataKey := strings.TrimSpace(m.cfg.GitAuthMetadataKey)
	if metadataKey == "" {
		metadataKey = defaultGitAuthMetaKey
	}
	if metadata != nil {
		if secretName := strings.TrimSpace(metadata[metadataKey]); secretName != "" {
			return secretName
		}
		if secretName := strings.TrimSpace(metadata["git_secret_name"]); secretName != "" {
			return secretName
		}
	}
	return strings.TrimSpace(m.cfg.GitAuthSecretName)
}

func (m *KubernetesManager) resolveRunnerImage(metadata map[string]string) (string, error) {
	defaultImage := strings.TrimSpace(m.cfg.PodImage)
	profileKey := strings.TrimSpace(m.cfg.RunnerProfileKey)
	if profileKey == "" || metadata == nil {
		return defaultImage, nil
	}

	profile := strings.ToLower(strings.TrimSpace(metadata[profileKey]))
	if profile == "" {
		return defaultImage, nil
	}
	if m.cfg.RunnerImageBundles == nil {
		return "", fmt.Errorf("runner profile %q requested but no runner image bundles are configured", profile)
	}
	image, found := m.cfg.RunnerImageBundles[profile]
	if !found || strings.TrimSpace(image) == "" {
		return "", fmt.Errorf("runner profile %q is not configured", profile)
	}
	return strings.TrimSpace(image), nil
}

func normalizeRunnerImageBundles(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}

	normalized := make(map[string]string, len(input))
	for key, value := range input {
		trimmedKey := strings.ToLower(strings.TrimSpace(key))
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		normalized[trimmedKey] = trimmedValue
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func sanitizeLabelValue(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "unknown"
	}
	invalid := regexp.MustCompile(`[^a-z0-9\-_.]`)
	value = invalid.ReplaceAllString(value, "-")
	if len(value) > 63 {
		value = value[:63]
	}
	value = strings.Trim(value, "-_.")
	if value == "" {
		return "unknown"
	}
	return value
}

func podNameFromSessionID(prefix, sessionID string) string {
	if strings.TrimSpace(prefix) == "" {
		prefix = defaultK8sPodNamePrefix
	}
	normalized := strings.ToLower(strings.TrimSpace(sessionID))
	invalid := regexp.MustCompile(`[^a-z0-9\-]`)
	normalized = invalid.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		normalized = "session"
	}

	maxSuffixLen := 63 - len(prefix) - 1
	if maxSuffixLen < 1 {
		maxSuffixLen = 1
	}
	if len(normalized) > maxSuffixLen {
		normalized = normalized[:maxSuffixLen]
	}
	return prefix + "-" + normalized
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func newK8sSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "session-fallback"
	}
	return "session-" + hex.EncodeToString(buf)
}

func boolPtr(value bool) *bool {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}
