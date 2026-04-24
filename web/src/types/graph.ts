// TS mirror of internal/graph/types.go. Keep in sync by hand for now;
// if this drifts often we'll generate from Go (e.g. with tygo).

export interface Scope {
  namespaces: string[];
}

export interface GraphNode {
  id: string;
  kind: string;
  name: string;
  namespace?: string;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  kind: string;
}

export interface Snapshot {
  scope: Scope;
  timestamp: string; // RFC3339 from Go's time.Time
  nodes: GraphNode[];
  edges: GraphEdge[];
}
