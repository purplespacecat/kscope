package graph

import (
	"context"
	"fmt"
	"time"
)

// Discover runs one parameterized discovery pass.
//
// v1 is a stub: it fabricates a tiny graph per selected namespace so the UI
// end-to-end flow (run → render → persist → refresh page) can be demonstrated
// without pulling in client-go. The real implementation will replace this
// function's body; the signature is stable.
func Discover(_ context.Context, scope Scope) (Snapshot, error) {
	if len(scope.Namespaces) == 0 {
		return Snapshot{}, fmt.Errorf("scope must include at least one namespace")
	}

	var nodes []Node
	var edges []Edge
	for _, ns := range scope.Namespaces {
		nsID := "ns/" + ns
		deployID := "deploy/" + ns + "/demo"
		podID := "pod/" + ns + "/demo-0"

		nodes = append(nodes,
			Node{ID: nsID, Kind: "Namespace", Name: ns},
			Node{ID: deployID, Kind: "Deployment", Name: "demo", Namespace: ns},
			Node{ID: podID, Kind: "Pod", Name: "demo-0", Namespace: ns},
		)
		edges = append(edges,
			Edge{ID: nsID + "->" + deployID, Source: nsID, Target: deployID, Kind: "contains"},
			Edge{ID: deployID + "->" + podID, Source: deployID, Target: podID, Kind: "owns"},
		)
	}

	return Snapshot{
		Scope:     scope,
		Timestamp: time.Now().UTC(),
		Nodes:     nodes,
		Edges:     edges,
	}, nil
}

// ListNamespaces returns the namespace choices shown in the scope picker.
// Stubbed for v1. Replaced by client-go call later.
func ListNamespaces(_ context.Context) ([]string, error) {
	return []string{"default", "kube-system", "monitoring", "crossplane-system"}, nil
}
