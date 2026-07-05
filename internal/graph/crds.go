package graph

import (
	"context"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// crdGVR is the well-known GVR of CRD definitions themselves.
var crdGVR = schema.GroupVersionResource{
	Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
}

// crdGroupID is the synthetic tree node grouping CRD definitions under the
// cluster — same pattern as the control-plane group, keeps the cluster fan
// readable.
const crdGroupID = "crds"

// customResources implements the generic CRD pass (spec §8.5): every CRD's
// instances become nodes with instance-of edges back to their definition.
//
// Listing strategy: one cluster-wide list per CRD (instead of one per
// namespace), filtered to the scope in-process — 40 CRDs means 40 calls, not
// 40×N. Only CRDs with at least one in-scope instance become nodes, mirroring
// the PV/StorageClass pruning: a namespaced invocation stays lean.
func (b *builder) customResources(ctx context.Context, dyn dynamic.Interface, wanted map[string]bool) {
	if dyn == nil {
		return // tests without a dynamic client
	}
	crdList, err := dyn.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			b.errf("customresourcedefinitions: %v", err)
		}
		return
	}

	groupPushed := false
	for i := range crdList.Items {
		crd := &crdList.Items[i]
		group, _, _ := unstructured.NestedString(crd.Object, "spec", "group")
		if fluxGroups[group] {
			continue // the Flux pass models these with richer semantics
		}
		kind, _, _ := unstructured.NestedString(crd.Object, "spec", "names", "kind")
		plural, _, _ := unstructured.NestedString(crd.Object, "spec", "names", "plural")
		scopeStr, _, _ := unstructured.NestedString(crd.Object, "spec", "scope")
		version := servedVersion(crd)
		if group == "" || kind == "" || plural == "" || version == "" {
			continue
		}

		gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: plural}
		list, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			b.errf("%s.%s: %v", plural, group, err)
			continue
		}

		namespaced := scopeStr == "Namespaced"
		crdID := "" // definition node is pushed lazily, on the first in-scope instance
		for j := range list.Items {
			u := &list.Items[j]
			if namespaced && len(wanted) > 0 && !wanted[u.GetNamespace()] {
				continue
			}
			if crdID == "" {
				if !groupPushed {
					b.pushCRDGroup()
					groupPushed = true
				}
				crdID = b.pushCRD(crd)
			}
			b.pushCR(u, gvr, kind, namespaced, crdID)
		}
	}
}

func (b *builder) pushCRDGroup() {
	b.push(Node{
		ID:        crdGroupID,
		Kind:      "CRDGroup",
		Name:      "custom resources",
		ParentID:  clusterNodeID,
		Health:    HealthHealthy,
		Synthetic: true,
		Kubectl:   kubectlCmd(b.kubectx, "", "get crds"),
	}, "", "")
}

func (b *builder) pushCRD(crd *unstructured.Unstructured) string {
	name := crd.GetName() // "<plural>.<group>", e.g. certificates.cert-manager.io
	id := nodeID("apiextensions.k8s.io", "CustomResourceDefinition", "", name)
	if b.ids[id] {
		return id
	}
	b.push(Node{
		ID:         id,
		Kind:       "CustomResourceDefinition",
		Name:       name,
		APIVersion: "apiextensions.k8s.io/v1",
		UID:        string(crd.GetUID()),
		ParentID:   crdGroupID,
		Labels:     crd.GetLabels(),
		Health:     conditionHealth(crd, "Established"),
		Kubectl:    kubectlCmd(b.kubectx, "", "get crd "+name+" -o yaml"),
	}, crd.GetUID(), "")
	b.captureManifest(id, crd.DeepCopy(), "apiextensions.k8s.io/v1", "CustomResourceDefinition")
	return id
}

func (b *builder) pushCR(u *unstructured.Unstructured, gvr schema.GroupVersionResource, kind string, namespaced bool, crdID string) {
	ns := u.GetNamespace()
	id := nodeID(gvr.Group, kind, ns, u.GetName())
	if b.ids[id] {
		return
	}
	// "<plural>.<group>" disambiguates kinds that exist in several groups
	// (two installed operators can both define a "Certificate").
	resource := gvr.Resource + "." + gvr.Group

	parent := ""
	if !namespaced {
		// Cluster-scoped instances (e.g. ClusterIssuers) have no namespace
		// home — their definition is the natural tree parent.
		parent = crdID
	}
	b.push(Node{
		ID:         id,
		Kind:       kind,
		Name:       u.GetName(),
		Namespace:  ns,
		APIVersion: gvr.GroupVersion().String(),
		UID:        string(u.GetUID()),
		ParentID:   parent,
		Labels:     u.GetLabels(),
		Health:     conditionHealth(u, "Ready", "Available", "Reconciled"),
		Kubectl:    kubectlCmd(b.kubectx, ns, "get "+resource+" "+u.GetName()+" -o yaml"),
	}, u.GetUID(), controllerUID(u.GetOwnerReferences()))
	b.captureManifest(id, u.DeepCopy(), gvr.GroupVersion().String(), kind)
	b.addEdge(EdgeInstanceOf, id, crdID)
}

// servedVersion picks the CRD's storage version, falling back to the first
// served one — what the API returns for unversioned reads.
func servedVersion(crd *unstructured.Unstructured) string {
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	firstServed := ""
	for _, v := range versions {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		served, _ := m["served"].(bool)
		storage, _ := m["storage"].(bool)
		if !served || name == "" {
			continue
		}
		if storage {
			return name
		}
		if firstServed == "" {
			firstServed = name
		}
	}
	return firstServed
}

// conditionHealth reads the first recognized status condition, trying the
// given types in priority order — different operators speak different
// dialects ("Ready" for cert-manager/Flux, "Available"/"Reconciled" for
// prometheus-operator, "Established" for CRDs). Objects that report no
// conditions at all count as healthy — like ConfigMaps, existing is their
// whole job, and grading them "unknown" would wash the tree health rollups
// gray. Only an object whose conditions match none of the sought types is
// truly unknown. All sought types are positive-polarity (True = good).
func conditionHealth(u *unstructured.Unstructured, condTypes ...string) Health {
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found || len(conds) == 0 {
		return HealthHealthy
	}
	for _, want := range condTypes {
		for _, c := range conds {
			m, ok := c.(map[string]any)
			if !ok || m["type"] != want {
				continue
			}
			switch m["status"] {
			case "True":
				return HealthHealthy
			case "False":
				return HealthError
			default:
				return HealthWarning
			}
		}
	}
	return suffixConditionHealth(conds)
}

// Positive-polarity condition-type suffixes, CamelCase. Operators invent
// component-specific types like metallb's "poolReconcilerValid" — the suffix
// still carries the meaning. Matching is case-SENSITIVE on purpose: inverted
// types ("Invalid", "NotReady") end in lowercase "valid"/"Ready"-after-"Not"
// and must not match.
var positiveConditionSuffixes = []string{
	"Valid", "Ready", "Available", "Established", "Loaded", "Succeeded", "Healthy",
}

// suffixConditionHealth is the fallback tier of conditionHealth: grade by
// recognizable positive suffixes, worst matched condition wins. Anything
// still unmatched stays honestly unknown.
func suffixConditionHealth(conds []any) Health {
	matched := false
	worst := HealthHealthy
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		condType, ok := m["type"].(string)
		if !ok || strings.HasPrefix(condType, "Not") {
			continue
		}
		for _, suffix := range positiveConditionSuffixes {
			if !strings.HasSuffix(condType, suffix) {
				continue
			}
			matched = true
			switch m["status"] {
			case "True":
				// stays at current worst
			case "False":
				worst = HealthError
			default:
				worst = worseHealth(worst, HealthWarning)
			}
			break
		}
	}
	if !matched {
		return HealthUnknown
	}
	return worst
}
