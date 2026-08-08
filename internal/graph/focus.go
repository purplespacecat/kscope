package graph

import "strings"

// NodeRef identifies a resource the way an outside tool knows it — the way k9s
// hands it over, for instance. It deliberately does not include the API group
// or a node ID: callers should not have to reconstruct kscope's ID format.
type NodeRef struct {
	// Namespace is empty for cluster-scoped resources.
	Namespace string
	Name      string
	// Kind is an optional disambiguator. It accepts either a Kind
	// ("Deployment") or a plural resource name ("deployments"), because
	// different tools supply different forms — k9s passes the plural.
	Kind string
}

// ResolveNode finds the node ID for a reference within a snapshot's nodes.
//
// Name must match exactly. Namespace must match when the reference supplies
// one. Kind is only consulted to break ties, because namespace+name is
// almost always unique on its own — and when it isn't (a Service and a
// Deployment commonly share a name), the kind hint settles it.
//
// Returns false when nothing matches, which is the normal case for a resource
// outside the current snapshot's scope.
func ResolveNode(nodes []Node, ref NodeRef) (string, bool) {
	if ref.Name == "" {
		return "", false
	}

	var matches []Node
	for _, n := range nodes {
		if n.Name != ref.Name {
			continue
		}
		if ref.Namespace != "" && n.Namespace != ref.Namespace {
			continue
		}
		matches = append(matches, n)
	}
	switch len(matches) {
	case 0:
		return "", false
	case 1:
		return matches[0].ID, true
	}

	if ref.Kind != "" {
		for _, n := range matches {
			if kindMatches(n.Kind, ref.Kind) {
				return n.ID, true
			}
		}
	}
	// Ambiguous and the hint didn't help. Returning the first match is better
	// than refusing: the user asked to look at something, and any of these is
	// a defensible landing spot they can navigate from.
	return matches[0].ID, true
}

// kindMatches compares a node's Kind against a hint that may be either the
// Kind itself or the plural resource name.
func kindMatches(kind, hint string) bool {
	k := strings.ToLower(kind)
	h := strings.ToLower(hint)
	return k == h || pluralize(k) == h
}

// pluralize applies the same rules the Kubernetes API machinery uses to derive
// a resource name from a kind: "deployment" → "deployments", "ingress" →
// "ingresses", "networkpolicy" → "networkpolicies".
func pluralize(kind string) string {
	switch {
	case strings.HasSuffix(kind, "s"), strings.HasSuffix(kind, "x"),
		strings.HasSuffix(kind, "ch"), strings.HasSuffix(kind, "sh"):
		return kind + "es"
	case strings.HasSuffix(kind, "y"):
		return strings.TrimSuffix(kind, "y") + "ies"
	default:
		return kind + "s"
	}
}
