package graph

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// controlPlaneID is the synthetic group node the logical components hang off.
const controlPlaneID = "control-plane"

// cpID builds a control-plane component node ID, e.g. "control-plane/etcd".
func cpID(slug string) string { return controlPlaneID + "/" + slug }

// controlPlane adds the logical control-plane: a group node plus components
// (api-server, datastore, scheduler, controller-manager) and the dependency
// spine between them (spec §4.4).
//
// These are Synthetic — on k3s the whole plane is one process, so there are
// no API objects to point at. On kubeadm-style clusters the components run
// as labeled static pods in kube-system; when found, their health is
// mirrored onto the matching logical component. On k3s none exist, and the
// components default to healthy: being able to run discovery at all means
// the plane answered.
func (b *builder) controlPlane(ctx context.Context, cs kubernetes.Interface, distro string) {
	b.push(Node{
		ID:        controlPlaneID,
		Kind:      "ControlPlane",
		Name:      "control-plane",
		ParentID:  clusterNodeID,
		Health:    HealthHealthy,
		Synthetic: true,
		Kubectl:   kubectlCmd(b.kubectx, "", "get --raw /readyz?verbose"),
	}, "", "")

	// Static-pod health, one cheap call independent of the namespace scope.
	compHealth := map[string]Health{}
	if pods, err := cs.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: "tier=control-plane"}); err != nil {
		b.errf("control-plane pods: %v", err)
	} else {
		for _, p := range pods.Items {
			comp := p.Labels["component"]
			if comp == "" {
				continue
			}
			h := podHealth(p)
			if prev, ok := compHealth[comp]; ok {
				h = worseHealth(prev, h) // HA planes: worst replica wins
			}
			compHealth[comp] = h
		}
	}

	datastoreName := "etcd"
	if distro == "k3s" {
		datastoreName = "datastore (kine)" // k3s embeds sqlite/kine unless configured with etcd
	}
	components := []struct {
		slug, name, staticPod, kubectl string
	}{
		{"api-server", "api-server", "kube-apiserver", "get --raw /readyz?verbose"},
		{"etcd", datastoreName, "etcd", "get --raw /readyz/etcd"},
		{"scheduler", "scheduler", "kube-scheduler", "get componentstatus scheduler"},
		{"controller-manager", "controller-manager", "kube-controller-manager", "get componentstatus controller-manager"},
	}
	for _, c := range components {
		health := HealthHealthy
		if h, ok := compHealth[c.staticPod]; ok {
			health = h
		}
		b.push(Node{
			ID:        cpID(c.slug),
			Kind:      "Component",
			Name:      c.name,
			ParentID:  controlPlaneID,
			Health:    health,
			Synthetic: true,
			Kubectl:   kubectlCmd(b.kubectx, "", c.kubectl),
		}, "", "")
	}

	// The dependency spine: scheduler and controller-manager act through the
	// API server; the API server persists everything to the datastore.
	b.addEdge(EdgeDependsOn, cpID("scheduler"), cpID("api-server"))
	b.addEdge(EdgeDependsOn, cpID("controller-manager"), cpID("api-server"))
	b.addEdge(EdgeDependsOn, cpID("api-server"), cpID("etcd"))
}

// clusterNodes adds the real machines. Must run after controlPlane so the
// node → api-server dependency edges find their target.
func (b *builder) clusterNodes(ctx context.Context, cs kubernetes.Interface) {
	list, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("nodes: %v", err)
		return
	}
	for _, n := range list.Items {
		id := nodeID("", "Node", "", n.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Node",
			Name:       n.Name,
			APIVersion: "v1",
			UID:        string(n.UID),
			ParentID:   clusterNodeID,
			Labels:     n.Labels,
			Health:     nodeHealth(n),
			Kubectl:    kubectlCmd(b.kubectx, "", "get node "+n.Name+" -o yaml"),
		}, n.UID, "")
		b.captureManifest(id, &n, "v1", "Node")
		// The kubelet registers with and watches the API server — this is the
		// "cluster depends on the api-server" relationship, drawn once per
		// machine instead of once per workload (which would be a hairball).
		b.addEdge(EdgeDependsOn, id, cpID("api-server"))
	}
}

// nodeHealth folds node conditions into one signal: not Ready is an error
// (Unknown means the node stopped reporting), resource pressure or a cordon
// is a warning.
func nodeHealth(n corev1.Node) Health {
	ready := corev1.ConditionUnknown
	pressure := false
	for _, c := range n.Status.Conditions {
		switch c.Type {
		case corev1.NodeReady:
			ready = c.Status
		case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
			if c.Status == corev1.ConditionTrue {
				pressure = true
			}
		}
	}
	switch {
	case ready != corev1.ConditionTrue:
		return HealthError
	case pressure || n.Spec.Unschedulable:
		return HealthWarning
	default:
		return HealthHealthy
	}
}

var healthRank = map[Health]int{
	HealthHealthy: 0,
	HealthUnknown: 1,
	HealthWarning: 2,
	HealthError:   3,
}

func worseHealth(a, b Health) Health {
	if healthRank[b] > healthRank[a] {
		return b
	}
	return a
}
