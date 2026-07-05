// TS mirror of internal/graph/types.go. Keep in sync by hand for now;
// if this drifts often we'll generate from Go (e.g. with tygo).

export interface Scope {
  namespaces: string[];
  /** Adds cluster Nodes + logical control-plane to the discovery. */
  includeInfra?: boolean;
  /** Adds generic custom resources (CRDs + their instances). */
  includeCRDs?: boolean;
}

export type Health = "healthy" | "warning" | "error" | "unknown";

export interface GraphNode {
  id: string;
  kind: string;
  name: string;
  namespace?: string;
  apiVersion?: string;
  uid?: string;
  /** Containment tree position; absent only for the cluster root. */
  parentId?: string;
  labels?: Record<string, string>;
  /** Absent on snapshots taken before milestone 1. */
  health?: Health;
  /** Logical node with no API object behind it (cluster root, k3s control-plane). */
  synthetic?: boolean;
  /** Copy-paste command to fetch this object live. */
  kubectl?: string;
  /** Set when a GitOps controller (Flux) manages this object. */
  gitops?: GitOpsRef;
  /** Clickable external references (e.g. a source repository). */
  links?: Link[];
}

export interface GitOpsRef {
  tool: string; // "flux"
  kind: string; // "Kustomization" | "HelmRelease"
  name: string;
  namespace: string;
  sourceRepo?: string;
  sourcePath?: string;
  revision?: string;
  webURL?: string;
}

export interface Link {
  label: string;
  url: string;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  kind: string;
}

export interface ClusterMeta {
  context: string;
  server: string;
  version: string;
  distro?: string;
}

export interface Stats {
  counts: Record<string, number>;
  durationMs: number;
  errors?: string[];
}

export interface Snapshot {
  scope: Scope;
  timestamp: string; // RFC3339 from Go's time.Time
  /** Absent on snapshots taken before milestone 1. */
  cluster?: ClusterMeta;
  nodes: GraphNode[];
  edges: GraphEdge[];
  stats?: Stats;
}
