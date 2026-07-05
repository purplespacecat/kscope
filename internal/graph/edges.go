package graph

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// inferEdges derives every cross-cutting relationship from the raw objects
// gathered during listing (spec §4.3). It runs after all nodes exist, because
// an edge is only recorded when both endpoints are in the snapshot — a
// reference to something out of scope is silently dropped rather than drawn
// as a broken edge. (TODO spec §4.3: surface dangling refs as a node badge.)
func (b *builder) inferEdges() {
	for _, p := range b.rawPods {
		b.podEdges(p)
	}
	for _, s := range b.rawServices {
		b.serviceEdges(s)
	}
	for _, ing := range b.rawIngresses {
		b.ingressEdges(ing)
	}
	for _, np := range b.rawNetpols {
		b.netpolEdges(np)
	}
	b.storageEdges()
}

// addEdge records src -kind-> tgt, dropping duplicates (a pod can mount the
// same ConfigMap through two volumes) and edges with out-of-scope endpoints.
func (b *builder) addEdge(kind, src, tgt string) {
	if !b.ids[src] || !b.ids[tgt] {
		return
	}
	id := src + " -" + kind + "-> " + tgt
	if b.edgeSeen[id] {
		return
	}
	b.edgeSeen[id] = true
	b.edges = append(b.edges, Edge{ID: id, Source: src, Target: tgt, Kind: kind})
}

// podEdges reads one pod spec — the richest source of wiring in Kubernetes:
// volumes, env, image pull secrets and the service account.
func (b *builder) podEdges(p corev1.Pod) {
	podID := nodeID("", "Pod", p.Namespace, p.Name)
	cm := func(name string) string { return nodeID("", "ConfigMap", p.Namespace, name) }
	sec := func(name string) string { return nodeID("", "Secret", p.Namespace, name) }

	for _, v := range p.Spec.Volumes {
		// The auto-injected service-account token volume would draw an edge
		// from every pod to kube-root-ca.crt — pure noise, skip it.
		if strings.HasPrefix(v.Name, "kube-api-access-") {
			continue
		}
		switch {
		case v.ConfigMap != nil:
			b.addEdge(EdgeMounts, podID, cm(v.ConfigMap.Name))
		case v.Secret != nil:
			b.addEdge(EdgeMounts, podID, sec(v.Secret.SecretName))
		case v.PersistentVolumeClaim != nil:
			b.addEdge(EdgeMounts, podID, nodeID("", "PersistentVolumeClaim", p.Namespace, v.PersistentVolumeClaim.ClaimName))
		case v.Projected != nil:
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					b.addEdge(EdgeMounts, podID, cm(src.ConfigMap.Name))
				}
				if src.Secret != nil {
					b.addEdge(EdgeMounts, podID, sec(src.Secret.Name))
				}
			}
		}
	}

	containers := make([]corev1.Container, 0, len(p.Spec.InitContainers)+len(p.Spec.Containers))
	containers = append(containers, p.Spec.InitContainers...)
	containers = append(containers, p.Spec.Containers...)
	for _, c := range containers {
		for _, e := range c.Env {
			if e.ValueFrom == nil {
				continue
			}
			if r := e.ValueFrom.ConfigMapKeyRef; r != nil {
				b.addEdge(EdgeReferences, podID, cm(r.Name))
			}
			if r := e.ValueFrom.SecretKeyRef; r != nil {
				b.addEdge(EdgeReferences, podID, sec(r.Name))
			}
		}
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				b.addEdge(EdgeReferences, podID, cm(ef.ConfigMapRef.Name))
			}
			if ef.SecretRef != nil {
				b.addEdge(EdgeReferences, podID, sec(ef.SecretRef.Name))
			}
		}
	}

	// Not in the spec table but the same trust relationship as env refs.
	for _, ips := range p.Spec.ImagePullSecrets {
		b.addEdge(EdgeReferences, podID, sec(ips.Name))
	}

	sa := p.Spec.ServiceAccountName
	if sa == "" {
		sa = "default" // what the API server defaults it to
	}
	b.addEdge(EdgeUses, podID, nodeID("", "ServiceAccount", p.Namespace, sa))
}

// serviceEdges matches a Service's selector against in-scope pods.
// A selector-less Service (headless with manual Endpoints, ExternalName)
// selects nothing — note this is the OPPOSITE of NetworkPolicy semantics.
func (b *builder) serviceEdges(s corev1.Service) {
	if len(s.Spec.Selector) == 0 {
		return
	}
	svcID := nodeID("", "Service", s.Namespace, s.Name)
	sel := labels.SelectorFromSet(s.Spec.Selector)
	for _, p := range b.rawPods {
		if p.Namespace != s.Namespace {
			continue
		}
		if sel.Matches(labels.Set(p.Labels)) {
			b.addEdge(EdgeSelects, svcID, nodeID("", "Pod", p.Namespace, p.Name))
		}
	}
}

func (b *builder) ingressEdges(ing networkingv1.Ingress) {
	ingID := nodeID("networking.k8s.io", "Ingress", ing.Namespace, ing.Name)
	backend := func(be *networkingv1.IngressBackend) {
		if be == nil || be.Service == nil {
			return
		}
		b.addEdge(EdgeExposes, ingID, nodeID("", "Service", ing.Namespace, be.Service.Name))
	}
	backend(ing.Spec.DefaultBackend)
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			backend(&path.Backend)
		}
	}
}

// netpolEdges matches a NetworkPolicy's podSelector against in-scope pods.
// NB: an EMPTY podSelector means "all pods in the namespace" — the opposite
// of a Service's empty selector. LabelSelectorAsSelector encodes exactly that.
func (b *builder) netpolEdges(np networkingv1.NetworkPolicy) {
	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
	if err != nil {
		b.errf("networkpolicy %s/%s selector: %v", np.Namespace, np.Name, err)
		return
	}
	npID := nodeID("networking.k8s.io", "NetworkPolicy", np.Namespace, np.Name)
	for _, p := range b.rawPods {
		if p.Namespace != np.Namespace {
			continue
		}
		if sel.Matches(labels.Set(p.Labels)) {
			b.addEdge(EdgeSelects, npID, nodeID("", "Pod", p.Namespace, p.Name))
		}
	}
}

// storageEdges walks PVC → PV → StorageClass. PVs and StorageClasses are
// cluster-scoped, so they only become nodes here, when an in-scope PVC
// actually reaches them — a namespaced invocation shouldn't drag in every
// volume in the cluster.
func (b *builder) storageEdges() {
	pvByName := make(map[string]corev1.PersistentVolume, len(b.rawPVs))
	for _, pv := range b.rawPVs {
		pvByName[pv.Name] = pv
	}

	for _, pvc := range b.rawPVCs {
		if pvc.Spec.VolumeName == "" {
			continue // unbound claim
		}
		pv, ok := pvByName[pvc.Spec.VolumeName]
		if !ok {
			continue
		}

		pvID := nodeID("", "PersistentVolume", "", pv.Name)
		if !b.ids[pvID] {
			b.push(Node{
				ID:         pvID,
				Kind:       "PersistentVolume",
				Name:       pv.Name,
				APIVersion: "v1",
				UID:        string(pv.UID),
				ParentID:   clusterNodeID,
				Labels:     pv.Labels,
				Health:     pvHealth(pv),
				Kubectl:    kubectlCmd(b.kubectx, "", "get pv "+pv.Name+" -o yaml"),
			}, pv.UID, "")
			b.captureManifest(pvID, &pv, "v1", "PersistentVolume")
		}
		b.addEdge(EdgeBinds, nodeID("", "PersistentVolumeClaim", pvc.Namespace, pvc.Name), pvID)

		scName := pv.Spec.StorageClassName
		if scName == "" {
			continue
		}
		sc, ok := b.rawSCs[scName]
		if !ok {
			continue
		}
		scID := nodeID("storage.k8s.io", "StorageClass", "", sc.Name)
		if !b.ids[scID] {
			b.push(Node{
				ID:         scID,
				Kind:       "StorageClass",
				Name:       sc.Name,
				APIVersion: "storage.k8s.io/v1",
				UID:        string(sc.UID),
				ParentID:   clusterNodeID,
				Labels:     sc.Labels,
				Health:     HealthHealthy,
				Kubectl:    kubectlCmd(b.kubectx, "", "get storageclass "+sc.Name+" -o yaml"),
			}, sc.UID, "")
			b.captureManifest(scID, &sc, "storage.k8s.io/v1", "StorageClass")
		}
		b.addEdge(EdgeBinds, pvID, scID)
	}
}
