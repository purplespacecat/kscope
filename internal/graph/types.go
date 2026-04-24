package graph

import "time"

// Scope describes what a discovery invocation should cover.
// v1 only filters by namespace; adding fields here stays backwards-compatible
// because the JSON decoder tolerates missing keys.
type Scope struct {
	Namespaces []string `json:"namespaces"`
}

// Node is a single resource in the graph. Kept intentionally small.
type Node struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"` // e.g. "owns", "contains"
}

// Snapshot is the complete result of one discovery invocation.
// The server keeps exactly one of these at a time.
type Snapshot struct {
	Scope     Scope     `json:"scope"`
	Timestamp time.Time `json:"timestamp"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
}
