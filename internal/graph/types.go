package graph

import "time"

// Scope describes what a discovery invocation should cover.
// v1 only filters by namespace; adding fields here stays backwards-compatible
// because the JSON decoder tolerates missing keys.
type Scope struct {
	// Namespaces to include. Empty means "every namespace in the cluster".
	Namespaces []string `json:"namespaces"`
	// IncludeInfra adds the infrastructure layer: cluster Nodes, the logical
	// control-plane components, and their depends-on/scheduled-on edges.
	IncludeInfra bool `json:"includeInfra,omitempty"`
}

// Health is a coarse rollup of an object's status, computed at discovery time
// so the UI never has to interpret kind-specific status fields.
type Health string

const (
	HealthHealthy Health = "healthy"
	HealthWarning Health = "warning"
	HealthError   Health = "error"
	HealthUnknown Health = "unknown"
)

// Node is a single resource in the graph.
//
// Containment (cluster → namespace → workload → pod) is expressed through
// ParentID — every node has exactly one place in the tree. Edges are reserved
// for cross-cutting relationships (mounts, selects, managed-by, ...) so the
// hierarchy stays unambiguous. See docs/spec-v1.md §3.
type Node struct {
	ID         string            `json:"id"` // "<group>/<kind>[/<ns>]/<name>", kind lowercased
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace,omitempty"`
	APIVersion string            `json:"apiVersion,omitempty"`
	UID        string            `json:"uid,omitempty"`
	ParentID   string            `json:"parentId,omitempty"` // "" only for the cluster root
	Labels     map[string]string `json:"labels,omitempty"`
	Health     Health            `json:"health"`
	// Synthetic marks logical nodes with no direct API object behind them
	// (the cluster root, and control-plane components on k3s where the whole
	// plane is one process). Rendered dashed in the UI.
	Synthetic bool   `json:"synthetic,omitempty"`
	Kubectl   string `json:"kubectl,omitempty"` // copy-paste command to fetch this object live
	// GitOps is set when a GitOps controller manages this object (detected
	// via Flux's ownership labels); nil otherwise.
	GitOps *GitOpsRef `json:"gitops,omitempty"`
	// Links are clickable external references (e.g. a source repository).
	Links []Link `json:"links,omitempty"`
}

// GitOpsRef records which GitOps object manages a resource and where its
// definition lives in Git (spec §4.5).
type GitOpsRef struct {
	Tool       string `json:"tool"`                 // "flux"
	Kind       string `json:"kind"`                 // "Kustomization" | "HelmRelease"
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	SourceRepo string `json:"sourceRepo,omitempty"` // git/oci/helm URL as declared
	SourcePath string `json:"sourcePath,omitempty"` // path within the source (or chart name)
	Revision   string `json:"revision,omitempty"`   // applied revision, e.g. "main@sha1:abc…"
	WebURL     string `json:"webURL,omitempty"`     // browsable tree link when the host is recognizable
}

// Link is a clickable external reference shown in the details panel.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Edge is a directed cross-cutting relationship between two nodes.
// Containment is NOT an edge — it lives in Node.ParentID.
type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// Edge kinds (spec §3).
const (
	EdgeMounts      = "mounts"       // Pod → ConfigMap/Secret/PVC via volumes
	EdgeReferences  = "references"   // Pod → ConfigMap/Secret via env/envFrom/imagePullSecrets
	EdgeUses        = "uses"         // Pod → ServiceAccount
	EdgeSelects     = "selects"      // Service/NetworkPolicy → Pod via label selector
	EdgeExposes     = "exposes"      // Ingress → Service
	EdgeBinds       = "binds"        // PVC → PV → StorageClass
	EdgeScheduledOn = "scheduled-on" // Pod → Node
	EdgeDependsOn   = "depends-on"   // Node → api-server → datastore; infra spine
	EdgeManagedBy   = "managed-by"   // resource → Flux Kustomization/HelmRelease
	EdgeSourcedFrom = "sourced-from" // Kustomization/HelmRelease → Git/OCI/Helm repository
)

// ClusterMeta identifies where a snapshot came from.
type ClusterMeta struct {
	Context string `json:"context"`          // kubeconfig context name
	Server  string `json:"server"`           // API server URL
	Version string `json:"version"`          // server GitVersion, e.g. "v1.33.6+k3s1"
	Distro  string `json:"distro,omitempty"` // "k3s" when detectable
}

// Stats summarizes one discovery pass, including anything that was skipped.
type Stats struct {
	Counts     map[string]int `json:"counts"`           // nodes per kind
	DurationMs int64          `json:"durationMs"`       // wall time of the pass
	Errors     []string       `json:"errors,omitempty"` // non-fatal skips (RBAC, missing APIs)
}

// Snapshot is the complete result of one discovery invocation.
// The server keeps exactly one of these at a time.
type Snapshot struct {
	Scope     Scope       `json:"scope"`
	Timestamp time.Time   `json:"timestamp"`
	Cluster   ClusterMeta `json:"cluster"`
	Nodes     []Node      `json:"nodes"`
	Edges     []Edge      `json:"edges"`
	Stats     Stats       `json:"stats"`

	// Manifests holds each node's redacted YAML, keyed by node ID. Excluded
	// from the snapshot JSON (it would dwarf the map): the store persists it
	// to a sidecar file and the server serves single entries on demand.
	Manifests map[string]string `json:"-"`
}
