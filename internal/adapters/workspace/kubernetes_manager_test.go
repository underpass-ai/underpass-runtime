package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
)

func TestKubernetesManager_CreateAndCloseSession(t *testing.T) {
	client := k8sfake.NewSimpleClientset()
	client.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction := action.(k8stesting.GetAction)
		tracked, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "tenant-runtime", getAction.GetName())
		if err != nil {
			return true, nil, err
		}
		pod := tracked.(*corev1.Pod).DeepCopy()
		pod.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}
		return true, pod, nil
	})

	manager := NewKubernetesManager(KubernetesManagerConfig{
		Namespace:       "tenant-runtime",
		PodReadyTimeout: 2 * time.Second,
	}, client)

	session, err := manager.CreateSession(context.Background(), app.CreateSessionRequest{
		SessionID:       "session-1",
		RepoURL:         "https://example.org/repo.git",
		RepoRef:         "main",
		ExpiresInSecond: 60,
		Principal:       domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
	})
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	if session.Runtime.Kind != domain.RuntimeKindKubernetes {
		t.Fatalf("unexpected runtime kind: %s", session.Runtime.Kind)
	}
	if session.Runtime.PodName == "" {
		t.Fatalf("expected runtime pod_name")
	}

	if err := manager.CloseSession(context.Background(), session.ID); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	_, err = client.CoreV1().Pods("tenant-runtime").Get(context.Background(), session.Runtime.PodName, metav1.GetOptions{})
	if err == nil {
		t.Fatalf("expected pod to be deleted")
	}
}

func TestKubernetesManager_CreateSessionRejectsSourceRepoPath(t *testing.T) {
	manager := NewKubernetesManager(KubernetesManagerConfig{}, k8sfake.NewSimpleClientset())
	_, err := manager.CreateSession(context.Background(), app.CreateSessionRequest{
		SourceRepoPath:  "/tmp/repo",
		ExpiresInSecond: 30,
		Principal:       domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
	})
	if err == nil {
		t.Fatal("expected source_repo_path rejection")
	}
}

func TestPodNameFromSessionID(t *testing.T) {
	name := podNameFromSessionID("ws", "Session_123")
	if strings.Contains(name, "_") {
		t.Fatalf("pod name must be sanitized: %s", name)
	}
	if !strings.HasPrefix(name, "ws-") {
		t.Fatalf("pod name must include prefix: %s", name)
	}
}

func TestKubernetesManager_SessionPodSecurityDefaultsAndGitSecret(t *testing.T) {
	manager := NewKubernetesManager(KubernetesManagerConfig{
		Namespace: "tenant-runtime",
	}, k8sfake.NewSimpleClientset())

	pod, err := manager.sessionPod(app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
		Metadata: map[string]string{
			"git_auth_secret": "tenant-git-auth",
		},
	}, "session-1", "ws-session-1")
	if err != nil {
		t.Fatalf("unexpected sessionPod error: %v", err)
	}

	if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.RunAsUser == nil {
		t.Fatal("expected pod security runAsUser to be set")
	}
	if *pod.Spec.SecurityContext.RunAsUser != defaultK8sRunAsUser {
		t.Fatalf("unexpected runAsUser: %d", *pod.Spec.SecurityContext.RunAsUser)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("expected automount service account token to default false")
	}

	initContainer := pod.Spec.InitContainers[0]
	foundGitAuthMount := false
	for _, mount := range initContainer.VolumeMounts {
		if mount.Name == "git-auth" && mount.MountPath == gitAuthMountPath {
			foundGitAuthMount = true
			break
		}
	}
	if !foundGitAuthMount {
		t.Fatal("expected git auth mount on init container")
	}

	foundGitAuthVolume := false
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == "git-auth" && volume.Secret != nil && volume.Secret.SecretName == "tenant-git-auth" {
			foundGitAuthVolume = true
			break
		}
	}
	if !foundGitAuthVolume {
		t.Fatal("expected git auth secret volume")
	}
}

func TestKubernetesManager_SessionPodUsesRunnerBundle(t *testing.T) {
	manager := NewKubernetesManager(KubernetesManagerConfig{
		Namespace: "tenant-runtime",
		PodImage:  "registry.example.com/runner/default:v1",
		RunnerImageBundles: map[string]string{
			"toolchains": "registry.example.com/runner/toolchains:v1",
		},
		RunnerProfileKey: "runner_profile",
	}, k8sfake.NewSimpleClientset())

	pod, err := manager.sessionPod(app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
		Metadata: map[string]string{
			"runner_profile": "toolchains",
		},
	}, "session-1", "ws-session-1")
	if err != nil {
		t.Fatalf("unexpected sessionPod error: %v", err)
	}
	if got := pod.Spec.Containers[0].Image; got != "registry.example.com/runner/toolchains:v1" {
		t.Fatalf("expected bundled runner image, got %q", got)
	}
}

func TestKubernetesManager_SessionPodRejectsUnknownRunnerBundle(t *testing.T) {
	manager := NewKubernetesManager(KubernetesManagerConfig{
		Namespace: "tenant-runtime",
		PodImage:  "registry.example.com/runner/default:v1",
		RunnerImageBundles: map[string]string{
			"toolchains": "registry.example.com/runner/toolchains:v1",
		},
		RunnerProfileKey: "runner_profile",
	}, k8sfake.NewSimpleClientset())

	_, err := manager.sessionPod(app.CreateSessionRequest{
		Principal: domain.Principal{TenantID: "tenant-a", ActorID: "alice"},
		Metadata: map[string]string{
			"runner_profile": "does-not-exist",
		},
	}, "session-1", "ws-session-1")
	if err == nil {
		t.Fatal("expected unknown runner profile rejection")
	}
	if !strings.Contains(err.Error(), "runner profile") {
		t.Fatalf("expected runner profile error, got %v", err)
	}
}

func TestKubernetesManager_CloseSessionFindsPodByLabel(t *testing.T) {
	sessionID := "session-label-lookup"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ws-session-label-lookup",
			Namespace: "tenant-runtime",
			Labels: map[string]string{
				"workspace_id": sanitizeLabelValue(sessionID),
			},
		},
	}
	client := k8sfake.NewSimpleClientset(pod)
	manager := NewKubernetesManager(KubernetesManagerConfig{
		Namespace: "tenant-runtime",
	}, client)

	if err := manager.CloseSession(context.Background(), sessionID); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), pod.Name, metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected pod to be deleted")
	}
}
