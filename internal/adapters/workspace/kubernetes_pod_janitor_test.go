package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/underpass-ai/underpass-runtime/internal/app"
	"github.com/underpass-ai/underpass-runtime/internal/domain"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

type fakeJanitorSessionStore struct {
	sessions map[string]domain.Session
	deleted  []string
}

func (s *fakeJanitorSessionStore) Save(_ context.Context, session domain.Session) error {
	if s.sessions == nil {
		s.sessions = map[string]domain.Session{}
	}
	s.sessions[session.ID] = session
	return nil
}

func (s *fakeJanitorSessionStore) Get(_ context.Context, sessionID string) (domain.Session, bool, error) {
	if s.sessions == nil {
		return domain.Session{}, false, nil
	}
	session, found := s.sessions[sessionID]
	return session, found, nil
}

func (s *fakeJanitorSessionStore) Delete(_ context.Context, sessionID string) error {
	if s.sessions != nil {
		delete(s.sessions, sessionID)
	}
	s.deleted = append(s.deleted, sessionID)
	return nil
}

func TestKubernetesPodJanitor_SweepDeletesTerminalContainerPods(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ws-ctr-session-a1b2c3",
			Namespace:         "tenant-runtime",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
			Labels: map[string]string{
				"app": "workspace-container-run",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	})

	janitor := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{
		Namespace:               "tenant-runtime",
		ContainerTerminalPodTTL: time.Minute,
	})
	janitor.Sweep(context.Background())

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), "ws-ctr-session-a1b2c3", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected terminal container pod to be deleted")
	}
}

func TestKubernetesPodJanitor_SweepDeletesOrphanedSessionPods(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ws-session-orphaned",
			Namespace:         "tenant-runtime",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
			Labels: map[string]string{
				"app":          "workspace-session",
				"workspace_id": "session-orphaned",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	store := &fakeJanitorSessionStore{sessions: map[string]domain.Session{}}
	janitor := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{
		Namespace:                 "tenant-runtime",
		SessionStore:              store,
		MissingSessionGracePeriod: time.Minute,
	})
	janitor.Sweep(context.Background())

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), "ws-session-orphaned", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected orphaned session pod to be deleted")
	}
}

func TestKubernetesPodJanitor_SweepKeepsFreshPodsDuringSessionGracePeriod(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ws-session-fresh",
			Namespace:         "tenant-runtime",
			CreationTimestamp: metav1.NewTime(now.Add(-15 * time.Second)),
			Labels: map[string]string{
				"app":          "workspace-session",
				"workspace_id": "session-fresh",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	store := &fakeJanitorSessionStore{sessions: map[string]domain.Session{}}
	janitor := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{
		Namespace:                 "tenant-runtime",
		SessionStore:              store,
		MissingSessionGracePeriod: time.Minute,
	})
	janitor.Sweep(context.Background())

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), "ws-session-fresh", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected fresh pod to remain during grace period: %v", err)
	}
}

func TestKubernetesPodJanitor_SweepDeletesExpiredSessionsAndStoreKey(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ws-session-expired",
			Namespace:         "tenant-runtime",
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Minute)),
			Labels: map[string]string{
				"app":          "workspace-session",
				"workspace_id": "session-expired",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	store := &fakeJanitorSessionStore{
		sessions: map[string]domain.Session{
			"session-expired": {
				ID:        "session-expired",
				ExpiresAt: now.Add(-time.Minute),
			},
		},
	}
	janitor := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{
		Namespace:                 "tenant-runtime",
		SessionStore:              store,
		MissingSessionGracePeriod: time.Minute,
	})
	janitor.Sweep(context.Background())

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), "ws-session-expired", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected expired session pod to be deleted")
	}

	if len(store.deleted) != 1 || store.deleted[0] != "session-expired" {
		t.Fatalf("expected expired session key to be deleted, got %#v", store.deleted)
	}
}

func TestKubernetesPodJanitor_SweepContainerPodsOrphanedSession(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ws-ctr-orphaned",
			Namespace:         "tenant-runtime",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
			Labels: map[string]string{
				"app":                  "workspace-container-run",
				"workspace_session_id": "session-orphaned",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	store := &fakeJanitorSessionStore{sessions: map[string]domain.Session{}}
	janitor := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{
		Namespace:                 "tenant-runtime",
		SessionStore:              store,
		ContainerTerminalPodTTL:   30 * time.Minute,
		MissingSessionGracePeriod: time.Minute,
	})
	janitor.Sweep(context.Background())

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), "ws-ctr-orphaned", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected orphaned container pod to be deleted when session not found past grace period")
	}
}

func TestKubernetesPodJanitor_SweepContainerPodsExpiredSession(t *testing.T) {
	now := time.Now().UTC()
	client := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ws-ctr-expired",
			Namespace:         "tenant-runtime",
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Minute)),
			Labels: map[string]string{
				"app":                  "workspace-container-run",
				"workspace_session_id": "session-expired",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	store := &fakeJanitorSessionStore{
		sessions: map[string]domain.Session{
			"session-expired": {
				ID:        "session-expired",
				ExpiresAt: now.Add(-time.Minute),
			},
		},
	}
	janitor := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{
		Namespace:               "tenant-runtime",
		SessionStore:            store,
		ContainerTerminalPodTTL: 30 * time.Minute,
	})
	janitor.Sweep(context.Background())

	_, err := client.CoreV1().Pods("tenant-runtime").Get(context.Background(), "ws-ctr-expired", metav1.GetOptions{})
	if err == nil {
		t.Fatal("expected expired-session container pod to be deleted")
	}
}

func TestKubernetesPodJanitor_SweepNilGuards(t *testing.T) {
	// nil janitor and nil client must not panic
	var nilJanitor *KubernetesPodJanitor
	nilJanitor.Sweep(context.Background())

	client := k8sfake.NewSimpleClientset()
	janitorNoNamespace := NewKubernetesPodJanitor(client, KubernetesPodJanitorConfig{Namespace: ""})
	janitorNoNamespace.Sweep(context.Background())

	janitorNilClient := NewKubernetesPodJanitor(nil, KubernetesPodJanitorConfig{Namespace: "ns"})
	janitorNilClient.Sweep(context.Background())
}

var _ app.SessionStore = (*fakeJanitorSessionStore)(nil)
