package graph

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func ptr[T any](v T) *T { return &v }

func activeNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
}

func indexByID(t *testing.T, snap Snapshot) map[string]Node {
	t.Helper()
	byID := make(map[string]Node, len(snap.Nodes))
	for _, n := range snap.Nodes {
		byID[n.ID] = n
	}
	return byID
}

// The canonical ownership chain: Deployment → ReplicaSet → Pod, all hanging
// under their namespace, which hangs under the cluster root.
func TestDiscover_BuildsOwnershipTree(t *testing.T) {
	deployUID := types.UID("uid-deploy")
	rsUID := types.UID("uid-rs")

	cs := fake.NewClientset(
		activeNamespace("app"),
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app", UID: deployUID},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr(int32(1))},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-abc", Namespace: "app", UID: rsUID,
				OwnerReferences: []metav1.OwnerReference{
					{UID: deployUID, Kind: "Deployment", Name: "web", Controller: ptr(true)},
				},
			},
			Spec:   appsv1.ReplicaSetSpec{Replicas: ptr(int32(1))},
			Status: appsv1.ReplicaSetStatus{Replicas: 1, ReadyReplicas: 1},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web-abc-xyz", Namespace: "app", UID: types.UID("uid-pod"),
				OwnerReferences: []metav1.OwnerReference{
					{UID: rsUID, Kind: "ReplicaSet", Name: "web-abc", Controller: ptr(true)},
				},
			},
			Status: corev1.PodStatus{
				Phase:             corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{Ready: true}},
			},
		},
	)

	snap, err := discover(context.Background(), cs, nil,ClusterMeta{Context: "test"}, Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)

	assertParent := func(id, wantParent string) {
		t.Helper()
		n, ok := byID[id]
		if !ok {
			t.Fatalf("node %q missing from snapshot", id)
		}
		if n.ParentID != wantParent {
			t.Fatalf("node %q parent = %q, want %q", id, n.ParentID, wantParent)
		}
	}

	assertParent("core/namespace/app", "cluster")
	assertParent("apps/deployment/app/web", "core/namespace/app")
	assertParent("apps/replicaset/app/web-abc", "apps/deployment/app/web")
	assertParent("core/pod/app/web-abc-xyz", "apps/replicaset/app/web-abc")

	if h := byID["apps/deployment/app/web"].Health; h != HealthHealthy {
		t.Fatalf("deployment health = %s, want healthy", h)
	}
	if got := byID["core/pod/app/web-abc-xyz"].Kubectl; got != "kubectl --context test -n app get pod web-abc-xyz -o yaml" {
		t.Fatalf("unexpected kubectl hint: %q", got)
	}
}

// A pod whose controller is out of scope (e.g. managed by a CRD we don't
// discover yet) must fall back to its namespace instead of dangling.
func TestDiscover_OwnerOutOfScopeFallsBackToNamespace(t *testing.T) {
	cs := fake.NewClientset(
		activeNamespace("app"),
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "orphan", Namespace: "app", UID: types.UID("uid-orphan"),
				OwnerReferences: []metav1.OwnerReference{
					{UID: types.UID("never-seen"), Kind: "HelmChart", Name: "x", Controller: ptr(true)},
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	snap, err := discover(context.Background(), cs, nil,ClusterMeta{}, Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)
	if got := byID["core/pod/app/orphan"].ParentID; got != "core/namespace/app" {
		t.Fatalf("orphan parent = %q, want the namespace", got)
	}
}

// Empty scope means "every namespace in the cluster" (spec §3).
func TestDiscover_EmptyScopeCoversAllNamespaces(t *testing.T) {
	cs := fake.NewClientset(activeNamespace("a"), activeNamespace("b"))

	snap, err := discover(context.Background(), cs, nil,ClusterMeta{}, Scope{})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)
	for _, id := range []string{"cluster", "core/namespace/a", "core/namespace/b"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected node %q in all-namespace snapshot", id)
		}
	}
}

// Scaled-to-zero ReplicaSets are rollout history, not live topology.
func TestDiscover_SkipsScaledDownReplicaSets(t *testing.T) {
	cs := fake.NewClientset(
		activeNamespace("app"),
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Name: "old-rev", Namespace: "app", UID: types.UID("uid-old")},
			Spec:       appsv1.ReplicaSetSpec{Replicas: ptr(int32(0))},
		},
	)

	snap, err := discover(context.Background(), cs, nil,ClusterMeta{}, Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if _, ok := indexByID(t, snap)["apps/replicaset/app/old-rev"]; ok {
		t.Fatal("scaled-down ReplicaSet should be skipped")
	}
	if snap.Stats.Counts["ReplicaSet"] != 0 {
		t.Fatalf("counts should not include skipped ReplicaSets: %v", snap.Stats.Counts)
	}
}

func TestPodHealth(t *testing.T) {
	running := func(statuses ...corev1.ContainerStatus) corev1.Pod {
		return corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: statuses}}
	}
	cases := []struct {
		name string
		pod  corev1.Pod
		want Health
	}{
		{"failed", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}}, HealthError},
		{"pending", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}, HealthWarning},
		{"succeeded", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}, HealthHealthy},
		{"running all ready", running(corev1.ContainerStatus{Ready: true}), HealthHealthy},
		{"running not ready", running(corev1.ContainerStatus{Ready: false}), HealthWarning},
		{
			"crashloop trumps running",
			running(corev1.ContainerStatus{
				Ready: false,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			}),
			HealthError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := podHealth(tc.pod); got != tc.want {
				t.Fatalf("podHealth = %s, want %s", got, tc.want)
			}
		})
	}
}
