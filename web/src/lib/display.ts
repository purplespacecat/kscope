import type { GitOpsRef, GraphNode, Health } from "../types/graph";

// Shared display vocabulary for the tree, graph, and details panel so a given
// kind or health state always looks the same everywhere.

const KIND_ABBREV: Record<string, string> = {
  Cluster: "CL",
  ControlPlane: "CP",
  Component: "CMP",
  Node: "NO",
  CRDGroup: "CRDS",
  CustomResourceDefinition: "CRD",
  StorageGroup: "STG",
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

// Distinct chip colors per kind, coherent by family: structural slate/sky,
// infra indigo/stone, workloads blue→cyan, batch yellow, config amber/rose,
// networking emerald/orange/red, storage teal, GitOps fuchsia, CRDs lime.
// Full literal class strings so Tailwind's scanner picks them up.
const KIND_CHIP: Record<string, string> = {
  Cluster: "bg-slate-200 text-slate-700",
  Namespace: "bg-sky-100 text-sky-700",
  ControlPlane: "bg-indigo-100 text-indigo-700",
  Component: "bg-indigo-50 text-indigo-600",
  Node: "bg-stone-200 text-stone-700",
  Deployment: "bg-blue-100 text-blue-700",
  ReplicaSet: "bg-blue-50 text-blue-600",
  StatefulSet: "bg-purple-100 text-purple-700",
  DaemonSet: "bg-violet-100 text-violet-700",
  Pod: "bg-cyan-100 text-cyan-700",
  Job: "bg-yellow-100 text-yellow-700",
  CronJob: "bg-yellow-100 text-yellow-800",
  Service: "bg-emerald-100 text-emerald-700",
  Ingress: "bg-orange-100 text-orange-700",
  NetworkPolicy: "bg-red-100 text-red-700",
  ConfigMap: "bg-amber-100 text-amber-700",
  Secret: "bg-rose-100 text-rose-700",
  ServiceAccount: "bg-green-100 text-green-700",
  PersistentVolumeClaim: "bg-teal-100 text-teal-700",
  PersistentVolume: "bg-teal-100 text-teal-800",
  StorageClass: "bg-teal-50 text-teal-600",
  StorageGroup: "bg-teal-100 text-teal-700",
  Kustomization: "bg-fuchsia-100 text-fuchsia-700",
  HelmRelease: "bg-fuchsia-100 text-fuchsia-800",
  GitRepository: "bg-pink-100 text-pink-700",
  OCIRepository: "bg-pink-100 text-pink-800",
  HelmRepository: "bg-pink-50 text-pink-600",
  CRDGroup: "bg-lime-100 text-lime-700",
  CustomResourceDefinition: "bg-lime-100 text-lime-800",
};

/** Chip colors for a kind; unknown kinds are custom resources → lime. */
export function kindChipClass(kind: string): string {
  return KIND_CHIP[kind] ?? "bg-lime-50 text-lime-700";
}

const KIND_PLURAL: Record<string, string> = {
  NetworkPolicy: "NetworkPolicies",
  Ingress: "Ingresses",
  StorageClass: "StorageClasses",
  HelmRepository: "HelmRepositories",
  GitRepository: "GitRepositories",
  OCIRepository: "OCIRepositories",
};

/** Plural display form for kind-group headers. */
export function kindPlural(kind: string): string {
  return KIND_PLURAL[kind] ?? `${kind}s`;
}

// Order children the way you'd scan a namespace: workloads first, their
// machinery after, then networking, config, and storage.
const KIND_ORDER = [
  "Cluster",
  "ControlPlane",
  "Component",
  "Node",
  "CRDGroup",
  "CustomResourceDefinition",
  "StorageGroup",
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

/**
 * Kinds that are infrastructure the cluster RUNS ON (vs content it hosts).
 * The graph ranks these above the cluster node — the "iceberg" layout.
 */
export const INFRA_KINDS = new Set(["ControlPlane", "Component", "Node"]);

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
  "instance-of": { stroke: "#65a30d" }, // lime — CR → its definition
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
  "instance-of": "instantiated by",
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
