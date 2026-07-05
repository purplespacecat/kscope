package graph

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}},
	}
}

func TestInfra_OffByDefault(t *testing.T) {
	cs := fake.NewClientset(activeNamespace("app"), readyNode("worker-1"))
	snap, err := discover(context.Background(), cs, nil, ClusterMeta{}, Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)
	for _, id := range []string{"core/node/worker-1", controlPlaneID, cpID("api-server")} {
		if _, ok := byID[id]; ok {
			t.Errorf("infra node %q present although IncludeInfra=false", id)
		}
	}
}

func TestInfra_NodesControlPlaneAndSpine(t *testing.T) {
	cs := fake.NewClientset(
		activeNamespace("app"),
		readyNode("worker-1"),
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"},
			Spec:       corev1.PodSpec{NodeName: "worker-1"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	snap, err := discover(context.Background(), cs, nil, ClusterMeta{Distro: "k3s"},
		Scope{Namespaces: []string{"app"}, IncludeInfra: true})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)

	node, ok := byID["core/node/worker-1"]
	if !ok || node.ParentID != clusterNodeID || node.Health != HealthHealthy {
		t.Fatalf("node wrong: %+v", node)
	}
	group, ok := byID[controlPlaneID]
	if !ok || !group.Synthetic || group.ParentID != clusterNodeID {
		t.Fatalf("control-plane group wrong: %+v", group)
	}
	for _, slug := range []string{"api-server", "etcd", "scheduler", "controller-manager"} {
		comp, ok := byID[cpID(slug)]
		if !ok || !comp.Synthetic || comp.ParentID != controlPlaneID {
			t.Fatalf("component %s wrong: %+v", slug, comp)
		}
	}
	// k3s naming for the datastore component.
	if byID[cpID("etcd")].Name != "datastore (kine)" {
		t.Fatalf("k3s datastore name: %q", byID[cpID("etcd")].Name)
	}

	edges := make(map[string]bool, len(snap.Edges))
	for _, e := range snap.Edges {
		edges[e.ID] = true
	}
	for _, id := range []string{
		"core/node/worker-1 -depends-on-> control-plane/api-server",
		"control-plane/api-server -depends-on-> control-plane/etcd",
		"control-plane/scheduler -depends-on-> control-plane/api-server",
		"control-plane/controller-manager -depends-on-> control-plane/api-server",
		"core/pod/app/web-0 -scheduled-on-> core/node/worker-1",
	} {
		if !edges[id] {
			t.Errorf("missing edge %q", id)
		}
	}
}

func TestInfra_StaticPodHealthMirrored(t *testing.T) {
	// kubeadm-style: a crashing kube-apiserver static pod must turn the
	// logical api-server component red — even though kube-system is not in
	// the discovery scope.
	cs := fake.NewClientset(
		activeNamespace("app"),
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kube-apiserver-master", Namespace: "kube-system",
				Labels: map[string]string{"tier": "control-plane", "component": "kube-apiserver"},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Ready: false,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				}},
			},
		},
	)
	snap, err := discover(context.Background(), cs, nil, ClusterMeta{},
		Scope{Namespaces: []string{"app"}, IncludeInfra: true})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)
	if h := byID[cpID("api-server")].Health; h != HealthError {
		t.Fatalf("api-server component health = %s, want error", h)
	}
	// Components without a static pod stay healthy.
	if h := byID[cpID("scheduler")].Health; h != HealthHealthy {
		t.Fatalf("scheduler component health = %s, want healthy", h)
	}
	// Non-k3s naming.
	if byID[cpID("etcd")].Name != "etcd" {
		t.Fatalf("datastore name = %q, want etcd", byID[cpID("etcd")].Name)
	}
}

func TestNodeHealth(t *testing.T) {
	cond := func(t corev1.NodeConditionType, s corev1.ConditionStatus) corev1.NodeCondition {
		return corev1.NodeCondition{Type: t, Status: s}
	}
	cases := []struct {
		name string
		node corev1.Node
		want Health
	}{
		{"ready", *readyNode("a"), HealthHealthy},
		{
			"not ready",
			corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{cond(corev1.NodeReady, corev1.ConditionFalse)}}},
			HealthError,
		},
		{
			"stopped reporting",
			corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{cond(corev1.NodeReady, corev1.ConditionUnknown)}}},
			HealthError,
		},
		{
			"memory pressure",
			corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				cond(corev1.NodeReady, corev1.ConditionTrue),
				cond(corev1.NodeMemoryPressure, corev1.ConditionTrue),
			}}},
			HealthWarning,
		},
		{
			"cordoned",
			corev1.Node{
				Spec:   corev1.NodeSpec{Unschedulable: true},
				Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{cond(corev1.NodeReady, corev1.ConditionTrue)}},
			},
			HealthWarning,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeHealth(tc.node); got != tc.want {
				t.Fatalf("nodeHealth = %s, want %s", got, tc.want)
			}
		})
	}
}
