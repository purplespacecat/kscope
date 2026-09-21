package graph

import "testing"

func TestIsDiscoveredKind(t *testing.T) {
	// k9s sends $RESOURCE_NAME, which is the plural; a hand-typed --focus-kind
	// is usually the Kind. Both have to work, via the same matcher ResolveNode
	// already uses as a tie-breaker.
	for _, hint := range []string{
		"Deployment", "deployments", "pod", "pods", "Secret", "secrets",
		"networkpolicies", "NetworkPolicy", "ingresses", "persistentvolumeclaims",
		"serviceaccounts", "statefulsets", "cronjobs", "namespaces",
	} {
		if !IsDiscoveredKind(hint) {
			t.Errorf("IsDiscoveredKind(%q) = false, want true", hint)
		}
	}

	// The Flux pass runs whether or not IncludeCRDs is set, so these are
	// discovered kinds. Treating them as custom resources would make every
	// GitOps handoff pay for a full CRD sweep it does not need.
	for _, hint := range []string{
		"kustomizations", "Kustomization", "helmreleases", "HelmRelease",
		"gitrepositories", "ocirepositories", "helmrepositories",
	} {
		if !IsDiscoveredKind(hint) {
			t.Errorf("IsDiscoveredKind(%q) = false, want true (Flux pass is unconditional)", hint)
		}
	}

	for _, hint := range []string{
		"managedresources", "compositions", "certificates", "ciliumidentities", "",
	} {
		if IsDiscoveredKind(hint) {
			t.Errorf("IsDiscoveredKind(%q) = true, want false", hint)
		}
	}
}

// Nodes come from the infra pass, which needs IncludeInfra — not IncludeCRDs.
// Getting this wrong would turn a node handoff into a custom-resource sweep
// that still could not find it.
func TestIsInfraKind(t *testing.T) {
	for _, hint := range []string{"Node", "nodes", "node"} {
		if !IsInfraKind(hint) {
			t.Errorf("IsInfraKind(%q) = false, want true", hint)
		}
	}
	if IsInfraKind("pods") {
		t.Error("IsInfraKind(pods) = true")
	}
	if IsDiscoveredKind("nodes") {
		t.Error("nodes is an infra kind, not an unconditionally discovered one")
	}
}

// Kinds kscope deliberately does not model. A focus on one can never resolve,
// so discovering first is pure waste — minutes of cluster reads to arrive at
// the same "not found".
func TestIsUnmappedKind(t *testing.T) {
	for _, hint := range []string{
		"endpoints", "Endpoints", "roles", "rolebindings", "clusterroles",
		"horizontalpodautoscalers", "poddisruptionbudgets", "resourcequotas", "events",
	} {
		if !IsUnmappedKind(hint) {
			t.Errorf("IsUnmappedKind(%q) = false, want true", hint)
		}
	}
	for _, hint := range []string{"pods", "deployments", "managedresources", ""} {
		if IsUnmappedKind(hint) {
			t.Errorf("IsUnmappedKind(%q) = true, want false", hint)
		}
	}
}

// A kind in both sets would make the decision order-dependent.
func TestKindSetsDoNotOverlap(t *testing.T) {
	for _, k := range unmappedKinds {
		if IsDiscoveredKind(k) || IsInfraKind(k) {
			t.Errorf("%q is listed as unmapped but also as discovered/infra", k)
		}
	}
}
