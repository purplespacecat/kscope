package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// clusterNodeID is the synthetic root of the containment tree. Exactly one
// cluster per snapshot in v1.
const clusterNodeID = "cluster"

// Discover runs one discovery pass against the cluster the active kubeconfig
// points at.
//
// Milestone 2 state: namespaces, core workloads, config/identity
// (ConfigMaps, Secrets, ServiceAccounts), networking (Services, Ingresses,
// NetworkPolicies) and storage (PVCs, plus referenced PVs/StorageClasses).
// Containment lives in Node.ParentID; cross-cutting relationships (mounts,
// selects, ...) are inferred into Edges. Every node's redacted manifest is
// captured into Snapshot.Manifests.
func Discover(ctx context.Context, scope Scope) (Snapshot, error) {
	kc, err := newKubeClient()
	if err != nil {
		return Snapshot{}, err
	}
	return discover(ctx, kc.clientset, kc.dynamic, kc.meta, scope)
}

// ListNamespaces returns the namespace choices shown in the scope picker.
func ListNamespaces(ctx context.Context) ([]string, error) {
	kc, err := newKubeClient()
	if err != nil {
		return nil, err
	}
	list, err := kc.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	sort.Strings(names)
	return names, nil
}

// discover is the testable core: it accepts any kubernetes.Interface (and
// dynamic.Interface for CRD-backed passes — nil skips them), so unit tests
// substitute fakes instead of a live cluster.
func discover(ctx context.Context, cs kubernetes.Interface, dyn dynamic.Interface, meta ClusterMeta, scope Scope) (Snapshot, error) {
	start := time.Now()
	b := &builder{
		kubectx:   meta.Context,
		edges:     []Edge{},
		byUID:     map[types.UID]string{},
		owner:     map[string]types.UID{},
		ids:       map[string]bool{},
		edgeSeen:  map[string]bool{},
		manifests: map[string]string{},
		counts:    map[string]int{},
		rawSCs:    map[string]storagev1.StorageClass{},
		rawFlux:   map[string]*unstructured.Unstructured{},
	}

	// Server version is best-effort — a snapshot without it is still useful.
	if v, err := cs.Discovery().ServerVersion(); err != nil {
		b.errf("server version: %v", err)
	} else {
		meta.Version = v.GitVersion
		if strings.Contains(v.GitVersion, "k3s") {
			meta.Distro = "k3s"
		}
	}

	// The cluster root everything hangs off.
	rootName := meta.Context
	if rootName == "" {
		rootName = "cluster"
	}
	b.push(Node{
		ID:      clusterNodeID,
		Kind:    "Cluster",
		Name:    rootName,
		Health:  HealthHealthy,
		Kubectl: kubectlCmd(b.kubectx, "", "cluster-info"),
	}, "", "")

	// Namespaces define the iteration scope for everything below, so this is
	// the one list we can't shrug off.
	nsList, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Snapshot{}, fmt.Errorf("list namespaces: %w", err)
	}
	wanted := map[string]bool{}
	for _, name := range scope.Namespaces {
		wanted[name] = true
	}

	var selected []string
	for _, ns := range nsList.Items {
		if len(wanted) > 0 && !wanted[ns.Name] {
			continue
		}
		health := HealthHealthy
		if ns.Status.Phase != corev1.NamespaceActive {
			health = HealthWarning // Terminating
		}
		id := nsID(ns.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Namespace",
			Name:       ns.Name,
			APIVersion: "v1",
			UID:        string(ns.UID),
			ParentID:   clusterNodeID,
			Labels:     ns.Labels,
			Health:     health,
			Kubectl:    kubectlCmd(b.kubectx, "", "get namespace "+ns.Name+" -o yaml"),
		}, ns.UID, "")
		b.captureManifest(id, &ns, "v1", "Namespace")
		selected = append(selected, ns.Name)
	}
	sort.Strings(selected)

	// One list call per (kind, namespace). Each failure is recorded and
	// skipped — a partial map beats no map on RBAC-restricted clusters.
	for _, ns := range selected {
		b.deployments(ctx, cs, ns)
		b.statefulSets(ctx, cs, ns)
		b.daemonSets(ctx, cs, ns)
		b.replicaSets(ctx, cs, ns)
		b.cronJobs(ctx, cs, ns)
		b.jobs(ctx, cs, ns)
		b.pods(ctx, cs, ns)
		b.configMaps(ctx, cs, ns)
		b.secrets(ctx, cs, ns)
		b.serviceAccounts(ctx, cs, ns)
		b.services(ctx, cs, ns)
		b.ingresses(ctx, cs, ns)
		b.networkPolicies(ctx, cs, ns)
		b.persistentVolumeClaims(ctx, cs, ns)
	}

	// Flux GitOps objects (spec §4.5) — via the dynamic client because
	// they're CRDs. Absent CRDs mean Flux isn't installed: skipped silently.
	b.fluxObjects(ctx, cs.Discovery(), dyn, selected)

	// Generic custom resources (spec §8.5): every other CRD's instances.
	if scope.IncludeCRDs {
		b.customResources(ctx, dyn, wanted)
	}

	// Cluster-scoped storage is listed once but NOT pushed wholesale — only
	// objects referenced by an in-scope PVC chain become nodes (§4.3), so a
	// namespace-scoped invocation stays namespace-scoped.
	if list, err := cs.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{}); err != nil {
		b.errf("persistentvolumes: %v", err)
	} else {
		b.rawPVs = list.Items
	}
	if list, err := cs.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{}); err != nil {
		b.errf("storageclasses: %v", err)
	} else {
		for _, sc := range list.Items {
			b.rawSCs[sc.Name] = sc
		}
	}

	// Infrastructure layer (spec §4.4): real nodes + a logical control-plane.
	// controlPlane must come first so node → api-server edges find a target.
	if scope.IncludeInfra {
		b.controlPlane(ctx, cs, meta.Distro)
		b.clusterNodes(ctx, cs)
	}

	// Owners may be listed after their children (or not at all), so parent
	// resolution is a second pass over the complete set.
	b.resolveParents()

	// Cross-cutting relationships need the full node set (an edge is only
	// added when both endpoints exist), so they come last. GitOps
	// back-references also walk every node to attach label-derived refs.
	b.fluxBackrefs()
	b.inferEdges()

	return Snapshot{
		Scope:     scope,
		Timestamp: time.Now().UTC(),
		Cluster:   meta,
		Nodes:     b.nodes,
		Edges:     b.edges,
		Stats: Stats{
			Counts:     b.counts,
			DurationMs: time.Since(start).Milliseconds(),
			Errors:     b.errs,
		},
		Manifests: b.manifests,
	}, nil
}

// builder accumulates nodes plus the bookkeeping needed to resolve the
// containment tree and infer edges once every object has been seen.
type builder struct {
	kubectx   string
	nodes     []Node
	edges     []Edge
	byUID     map[types.UID]string // object UID → node ID
	owner     map[string]types.UID // node ID → controller ownerReference UID
	ids       map[string]bool      // node IDs in the snapshot (edge scope checks)
	edgeSeen  map[string]bool      // edge ID dedupe
	manifests map[string]string    // node ID → redacted YAML
	counts    map[string]int
	errs      []string

	// Raw objects retained for the edge-inference pass (spec §4.3).
	rawPods      []corev1.Pod
	rawServices  []corev1.Service
	rawIngresses []networkingv1.Ingress
	rawNetpols   []networkingv1.NetworkPolicy
	rawPVCs      []corev1.PersistentVolumeClaim
	rawPVs       []corev1.PersistentVolume
	rawSCs       map[string]storagev1.StorageClass
	rawFlux      map[string]*unstructured.Unstructured // node ID → toolkit CR
}

func (b *builder) push(n Node, uid, ownerUID types.UID) {
	b.nodes = append(b.nodes, n)
	b.counts[n.Kind]++
	b.ids[n.ID] = true
	if uid != "" {
		b.byUID[uid] = n.ID
	}
	if ownerUID != "" {
		b.owner[n.ID] = ownerUID
	}
}

func (b *builder) errf(format string, args ...any) {
	b.errs = append(b.errs, fmt.Sprintf(format, args...))
}

// captureManifest renders and stores the redacted YAML for one node.
// A manifest failure never fails discovery — the node just has no manifest.
func (b *builder) captureManifest(id string, obj any, apiVersion, kind string) {
	y, err := manifestYAML(obj, apiVersion, kind)
	if err != nil {
		b.errf("manifest %s: %v", id, err)
		return
	}
	b.manifests[id] = y
}

// resolveParents fills every unset ParentID: the controller owner when it was
// discovered, otherwise the containing namespace. A pod whose owner is out of
// scope (e.g. managed by a CRD we don't know yet) still gets a truthful home.
func (b *builder) resolveParents() {
	for i := range b.nodes {
		n := &b.nodes[i]
		if n.ParentID != "" || n.ID == clusterNodeID {
			continue
		}
		if uid, ok := b.owner[n.ID]; ok {
			if parent, found := b.byUID[uid]; found {
				n.ParentID = parent
				continue
			}
		}
		if n.Namespace != "" {
			n.ParentID = nsID(n.Namespace)
		}
	}
}

// --- per-kind listers ------------------------------------------------------

func (b *builder) deployments(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("deployments %s: %v", ns, err)
		return
	}
	for _, d := range list.Items {
		id := nodeID("apps", "Deployment", ns, d.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Deployment",
			Name:       d.Name,
			Namespace:  ns,
			APIVersion: "apps/v1",
			UID:        string(d.UID),
			Labels:     d.Labels,
			Health:     replicaHealth(d.Spec.Replicas, d.Status.ReadyReplicas),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get deployment "+d.Name+" -o yaml"),
		}, d.UID, controllerUID(d.OwnerReferences))
		b.captureManifest(id, &d, "apps/v1", "Deployment")
	}
}

func (b *builder) statefulSets(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("statefulsets %s: %v", ns, err)
		return
	}
	for _, s := range list.Items {
		id := nodeID("apps", "StatefulSet", ns, s.Name)
		b.push(Node{
			ID:         id,
			Kind:       "StatefulSet",
			Name:       s.Name,
			Namespace:  ns,
			APIVersion: "apps/v1",
			UID:        string(s.UID),
			Labels:     s.Labels,
			Health:     replicaHealth(s.Spec.Replicas, s.Status.ReadyReplicas),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get statefulset "+s.Name+" -o yaml"),
		}, s.UID, controllerUID(s.OwnerReferences))
		b.captureManifest(id, &s, "apps/v1", "StatefulSet")
	}
}

func (b *builder) daemonSets(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("daemonsets %s: %v", ns, err)
		return
	}
	for _, d := range list.Items {
		id := nodeID("apps", "DaemonSet", ns, d.Name)
		b.push(Node{
			ID:         id,
			Kind:       "DaemonSet",
			Name:       d.Name,
			Namespace:  ns,
			APIVersion: "apps/v1",
			UID:        string(d.UID),
			Labels:     d.Labels,
			Health:     daemonHealth(d.Status),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get daemonset "+d.Name+" -o yaml"),
		}, d.UID, controllerUID(d.OwnerReferences))
		b.captureManifest(id, &d, "apps/v1", "DaemonSet")
	}
}

func (b *builder) replicaSets(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("replicasets %s: %v", ns, err)
		return
	}
	for _, rs := range list.Items {
		// Scaled-to-zero ReplicaSets are kept rollout history (one per past
		// revision, up to revisionHistoryLimit). They own no pods and bury
		// the live revision in noise, so they're skipped.
		if rs.Spec.Replicas != nil && *rs.Spec.Replicas == 0 && rs.Status.Replicas == 0 {
			continue
		}
		id := nodeID("apps", "ReplicaSet", ns, rs.Name)
		b.push(Node{
			ID:         id,
			Kind:       "ReplicaSet",
			Name:       rs.Name,
			Namespace:  ns,
			APIVersion: "apps/v1",
			UID:        string(rs.UID),
			Labels:     rs.Labels,
			Health:     replicaHealth(rs.Spec.Replicas, rs.Status.ReadyReplicas),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get replicaset "+rs.Name+" -o yaml"),
		}, rs.UID, controllerUID(rs.OwnerReferences))
		b.captureManifest(id, &rs, "apps/v1", "ReplicaSet")
	}
}

func (b *builder) cronJobs(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("cronjobs %s: %v", ns, err)
		return
	}
	for _, cj := range list.Items {
		health := HealthHealthy
		if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
			health = HealthWarning // paused — worth surfacing
		}
		id := nodeID("batch", "CronJob", ns, cj.Name)
		b.push(Node{
			ID:         id,
			Kind:       "CronJob",
			Name:       cj.Name,
			Namespace:  ns,
			APIVersion: "batch/v1",
			UID:        string(cj.UID),
			Labels:     cj.Labels,
			Health:     health,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get cronjob "+cj.Name+" -o yaml"),
		}, cj.UID, controllerUID(cj.OwnerReferences))
		b.captureManifest(id, &cj, "batch/v1", "CronJob")
	}
}

func (b *builder) jobs(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("jobs %s: %v", ns, err)
		return
	}
	for _, j := range list.Items {
		id := nodeID("batch", "Job", ns, j.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Job",
			Name:       j.Name,
			Namespace:  ns,
			APIVersion: "batch/v1",
			UID:        string(j.UID),
			Labels:     j.Labels,
			Health:     jobHealth(j),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get job "+j.Name+" -o yaml"),
		}, j.UID, controllerUID(j.OwnerReferences))
		b.captureManifest(id, &j, "batch/v1", "Job")
	}
}

func (b *builder) pods(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("pods %s: %v", ns, err)
		return
	}
	for _, p := range list.Items {
		id := nodeID("", "Pod", ns, p.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Pod",
			Name:       p.Name,
			Namespace:  ns,
			APIVersion: "v1",
			UID:        string(p.UID),
			Labels:     p.Labels,
			Health:     podHealth(p),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get pod "+p.Name+" -o yaml"),
		}, p.UID, controllerUID(p.OwnerReferences))
		b.captureManifest(id, &p, "v1", "Pod")
	}
	b.rawPods = append(b.rawPods, list.Items...)
}

// Config, identity, networking and storage kinds carry no meaningful status
// of their own — existing is fine, so they're "healthy" rather than
// "unknown". That keeps tree health rollups driven by things that can
// actually break (workloads, pods, PVCs).

func (b *builder) configMaps(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("configmaps %s: %v", ns, err)
		return
	}
	for _, cm := range list.Items {
		id := nodeID("", "ConfigMap", ns, cm.Name)
		b.push(Node{
			ID:         id,
			Kind:       "ConfigMap",
			Name:       cm.Name,
			Namespace:  ns,
			APIVersion: "v1",
			UID:        string(cm.UID),
			Labels:     cm.Labels,
			Health:     HealthHealthy,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get configmap "+cm.Name+" -o yaml"),
		}, cm.UID, controllerUID(cm.OwnerReferences))
		b.captureManifest(id, &cm, "v1", "ConfigMap")
	}
}

func (b *builder) secrets(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("secrets %s: %v", ns, err)
		return
	}
	for _, s := range list.Items {
		// Node carries name/labels only; the manifest is redacted to keys.
		// Secret VALUES are never stored anywhere (spec §3.1).
		id := nodeID("", "Secret", ns, s.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Secret",
			Name:       s.Name,
			Namespace:  ns,
			APIVersion: "v1",
			UID:        string(s.UID),
			Labels:     s.Labels,
			Health:     HealthHealthy,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get secret "+s.Name+" -o yaml"),
		}, s.UID, controllerUID(s.OwnerReferences))
		b.captureManifest(id, &s, "v1", "Secret")
	}
}

func (b *builder) serviceAccounts(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("serviceaccounts %s: %v", ns, err)
		return
	}
	for _, sa := range list.Items {
		id := nodeID("", "ServiceAccount", ns, sa.Name)
		b.push(Node{
			ID:         id,
			Kind:       "ServiceAccount",
			Name:       sa.Name,
			Namespace:  ns,
			APIVersion: "v1",
			UID:        string(sa.UID),
			Labels:     sa.Labels,
			Health:     HealthHealthy,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get serviceaccount "+sa.Name+" -o yaml"),
		}, sa.UID, controllerUID(sa.OwnerReferences))
		b.captureManifest(id, &sa, "v1", "ServiceAccount")
	}
}

func (b *builder) services(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("services %s: %v", ns, err)
		return
	}
	for _, s := range list.Items {
		id := nodeID("", "Service", ns, s.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Service",
			Name:       s.Name,
			Namespace:  ns,
			APIVersion: "v1",
			UID:        string(s.UID),
			Labels:     s.Labels,
			Health:     HealthHealthy,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get service "+s.Name+" -o yaml"),
		}, s.UID, controllerUID(s.OwnerReferences))
		b.captureManifest(id, &s, "v1", "Service")
	}
	b.rawServices = append(b.rawServices, list.Items...)
}

func (b *builder) ingresses(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("ingresses %s: %v", ns, err)
		return
	}
	for _, ing := range list.Items {
		id := nodeID("networking.k8s.io", "Ingress", ns, ing.Name)
		b.push(Node{
			ID:         id,
			Kind:       "Ingress",
			Name:       ing.Name,
			Namespace:  ns,
			APIVersion: "networking.k8s.io/v1",
			UID:        string(ing.UID),
			Labels:     ing.Labels,
			Health:     HealthHealthy,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get ingress "+ing.Name+" -o yaml"),
		}, ing.UID, controllerUID(ing.OwnerReferences))
		b.captureManifest(id, &ing, "networking.k8s.io/v1", "Ingress")
	}
	b.rawIngresses = append(b.rawIngresses, list.Items...)
}

func (b *builder) networkPolicies(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("networkpolicies %s: %v", ns, err)
		return
	}
	for _, np := range list.Items {
		id := nodeID("networking.k8s.io", "NetworkPolicy", ns, np.Name)
		b.push(Node{
			ID:         id,
			Kind:       "NetworkPolicy",
			Name:       np.Name,
			Namespace:  ns,
			APIVersion: "networking.k8s.io/v1",
			UID:        string(np.UID),
			Labels:     np.Labels,
			Health:     HealthHealthy,
			Kubectl:    kubectlCmd(b.kubectx, ns, "get networkpolicy "+np.Name+" -o yaml"),
		}, np.UID, controllerUID(np.OwnerReferences))
		b.captureManifest(id, &np, "networking.k8s.io/v1", "NetworkPolicy")
	}
	b.rawNetpols = append(b.rawNetpols, list.Items...)
}

func (b *builder) persistentVolumeClaims(ctx context.Context, cs kubernetes.Interface, ns string) {
	list, err := cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		b.errf("persistentvolumeclaims %s: %v", ns, err)
		return
	}
	for _, pvc := range list.Items {
		id := nodeID("", "PersistentVolumeClaim", ns, pvc.Name)
		b.push(Node{
			ID:         id,
			Kind:       "PersistentVolumeClaim",
			Name:       pvc.Name,
			Namespace:  ns,
			APIVersion: "v1",
			UID:        string(pvc.UID),
			Labels:     pvc.Labels,
			Health:     pvcHealth(pvc),
			Kubectl:    kubectlCmd(b.kubectx, ns, "get pvc "+pvc.Name+" -o yaml"),
		}, pvc.UID, controllerUID(pvc.OwnerReferences))
		b.captureManifest(id, &pvc, "v1", "PersistentVolumeClaim")
	}
	b.rawPVCs = append(b.rawPVCs, list.Items...)
}

// --- health rollups --------------------------------------------------------

// replicaHealth is the shared rollup for replica-managed workloads: all ready
// → healthy, some → warning, none (of a non-zero ask) → error.
func replicaHealth(desired *int32, ready int32) Health {
	want := int32(1) // k8s defaults spec.replicas to 1 when unset
	if desired != nil {
		want = *desired
	}
	switch {
	case want == 0:
		return HealthHealthy // deliberately scaled down
	case ready == 0:
		return HealthError
	case ready < want:
		return HealthWarning
	default:
		return HealthHealthy
	}
}

// daemonHealth compares scheduled vs ready daemon pods.
func daemonHealth(st appsv1.DaemonSetStatus) Health {
	switch {
	case st.DesiredNumberScheduled == 0:
		return HealthHealthy // nothing to run (no matching nodes)
	case st.NumberReady == 0:
		return HealthError
	case st.NumberReady < st.DesiredNumberScheduled:
		return HealthWarning
	default:
		return HealthHealthy
	}
}

func jobHealth(j batchv1.Job) Health {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobFailed:
			return HealthError
		case batchv1.JobComplete:
			return HealthHealthy
		}
	}
	if j.Status.Active > 0 {
		return HealthHealthy // still running
	}
	return HealthUnknown
}

// podHealth folds phase + container states into one signal. CrashLoopBackOff
// is an error even though the pod phase stays "Running".
func podHealth(p corev1.Pod) Health {
	switch p.Status.Phase {
	case corev1.PodSucceeded:
		return HealthHealthy
	case corev1.PodFailed:
		return HealthError
	case corev1.PodPending:
		return HealthWarning
	case corev1.PodRunning:
		for _, cst := range p.Status.ContainerStatuses {
			if w := cst.State.Waiting; w != nil && w.Reason == "CrashLoopBackOff" {
				return HealthError
			}
			if !cst.Ready {
				return HealthWarning
			}
		}
		return HealthHealthy
	default:
		return HealthUnknown
	}
}

func pvcHealth(pvc corev1.PersistentVolumeClaim) Health {
	switch pvc.Status.Phase {
	case corev1.ClaimBound:
		return HealthHealthy
	case corev1.ClaimPending:
		return HealthWarning
	case corev1.ClaimLost:
		return HealthError
	default:
		return HealthUnknown
	}
}

func pvHealth(pv corev1.PersistentVolume) Health {
	switch pv.Status.Phase {
	case corev1.VolumeBound, corev1.VolumeAvailable:
		return HealthHealthy
	case corev1.VolumeReleased:
		return HealthWarning
	case corev1.VolumeFailed:
		return HealthError
	default:
		return HealthUnknown
	}
}

// --- identity helpers ------------------------------------------------------

// nodeID builds the stable "<group>/<kind>[/<ns>]/<name>" identifier from the
// spec (§3). The core group is spelled "core" so IDs never start with "/";
// cluster-scoped kinds skip the namespace segment. IDs are reference keys
// only — they're never parsed back apart.
func nodeID(group, kind, ns, name string) string {
	if group == "" {
		group = "core"
	}
	if ns == "" {
		return group + "/" + strings.ToLower(kind) + "/" + name
	}
	return group + "/" + strings.ToLower(kind) + "/" + ns + "/" + name
}

func nsID(name string) string {
	return nodeID("", "Namespace", "", name)
}

// controllerUID returns the UID of the controlling owner, if any — Kubernetes
// marks at most one ownerReference per object with controller=true.
func controllerUID(refs []metav1.OwnerReference) types.UID {
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller {
			return ref.UID
		}
	}
	return ""
}

// kubectlCmd renders "kubectl [--context c] [-n ns] <rest>" — the copy-paste
// retrieval command surfaced in the UI next to each resource.
func kubectlCmd(kubectx, ns, rest string) string {
	var sb strings.Builder
	sb.WriteString("kubectl")
	if kubectx != "" {
		sb.WriteString(" --context " + kubectx)
	}
	if ns != "" {
		sb.WriteString(" -n " + ns)
	}
	sb.WriteString(" " + rest)
	return sb.String()
}
