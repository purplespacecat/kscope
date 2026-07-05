import type { GitOpsRef, GraphNode, Health } from "../types/graph";

// Shared display vocabulary for the tree, graph, and details panel so a given
// kind or health state always looks the same everywhere.

const KIND_ABBREV: Record<string, string> = {
  Cluster: "CL",
  ControlPlane: "CP",
  Component: "CMP",
  Node: "NO",
  Namespace: "NS",
  Deployment: "D",
  StatefulSet: "STS",
  DaemonSet: "DS",
  ReplicaSet: "RS",
  CronJob: "CJ",
  Job: "J",
  Pod: "P",
  Service: "SVC",
  Ingress: "ING",
  NetworkPolicy: "NP",
  Kustomization: "KS",
  HelmRelease: "HR",
  GitRepository: "GR",
  OCIRepository: "OR",
  HelmRepository: "HRP",
  ConfigMap: "CM",
  Secret: "SEC",
  ServiceAccount: "SA",
  PersistentVolumeClaim: "PVC",
  PersistentVolume: "PV",
  StorageClass: "SC",
};

export function kindAbbrev(kind: string): string {
  return KIND_ABBREV[kind] ?? kind.slice(0, 2).toUpperCase();
}

// Order children the way you'd scan a namespace: workloads first, their
// machinery after, then networking, config, and storage.
const KIND_ORDER = [
  "Cluster",
  "ControlPlane",
  "Component",
  "Node",
  "Namespace",
  "Deployment",
  "StatefulSet",
  "DaemonSet",
  "CronJob",
  "Job",
  "ReplicaSet",
  "Pod",
  "Kustomization",
  "HelmRelease",
  "GitRepository",
  "OCIRepository",
  "HelmRepository",
  "Service",
  "Ingress",
  "NetworkPolicy",
  "ConfigMap",
  "Secret",
  "ServiceAccount",
  "PersistentVolumeClaim",
  "PersistentVolume",
  "StorageClass",
];

export function kindRank(kind: string): number {
  const i = KIND_ORDER.indexOf(kind);
  return i === -1 ? KIND_ORDER.length : i;
}

/** Health with a fallback for pre-milestone-1 snapshots. */
export function health(n: GraphNode): Health {
  return n.health ?? "unknown";
}

// Severity ranking for rollups: a parent surfaces the worst health found in
// its subtree so problems are visible without expanding everything.
const SEVERITY: Record<Health, number> = {
  healthy: 0,
  unknown: 1,
  warning: 2,
  error: 3,
};

export function worseOf(a: Health, b: Health): Health {
  return SEVERITY[b] > SEVERITY[a] ? b : a;
}

// Tailwind classes for health dots; hex twins for React Flow inline styles.
export const HEALTH_DOT: Record<Health, string> = {
  healthy: "bg-emerald-500",
  warning: "bg-amber-500",
  error: "bg-red-500",
  unknown: "bg-slate-300",
};

export const HEALTH_HEX: Record<Health, string> = {
  healthy: "#10b981",
  warning: "#f59e0b",
  error: "#ef4444",
  unknown: "#cbd5e1",
};

export const HEALTH_LABEL: Record<Health, string> = {
  healthy: "Healthy",
  warning: "Warning",
  error: "Error",
  unknown: "Unknown",
};

// Relationship edges are a colored, dashed overlay — visually distinct from
// the plain containment lines whose meaning the layout already carries.
export const EDGE_STYLE: Record<string, { stroke: string }> = {
  mounts: { stroke: "#8b5cf6" }, // violet
  references: { stroke: "#3b82f6" }, // blue
  uses: { stroke: "#64748b" }, // slate
  selects: { stroke: "#10b981" }, // emerald
  exposes: { stroke: "#f97316" }, // orange
  binds: { stroke: "#0d9488" }, // teal
  "scheduled-on": { stroke: "#06b6d4" }, // cyan
  "depends-on": { stroke: "#6366f1" }, // indigo
  "managed-by": { stroke: "#d946ef" }, // fuchsia — GitOps
  "sourced-from": { stroke: "#ec4899" }, // pink — GitOps
};

const INCOMING_LABEL: Record<string, string> = {
  mounts: "mounted by",
  references: "referenced by",
  uses: "used by",
  selects: "selected by",
  exposes: "exposed by",
  binds: "bound by",
  "scheduled-on": "hosts",
  "depends-on": "depended on by",
  "managed-by": "manages",
  "sourced-from": "sources",
};

/** Label for an edge read from the target's side. */
export function incomingEdgeLabel(kind: string): string {
  return INCOMING_LABEL[kind] ?? `${kind} ←`;
}

/**
 * Node ID of the Flux object managing a resource — forward-constructed the
 * same way the backend builds IDs, so the GitOps card can link to it.
 */
export function gitopsManagerId(ref: GitOpsRef): string {
  const group =
    ref.kind === "Kustomization"
      ? "kustomize.toolkit.fluxcd.io"
      : "helm.toolkit.fluxcd.io";
  return `${group}/${ref.kind.toLowerCase()}/${ref.namespace}/${ref.name}`;
}
