import type { GitOpsRef, GraphNode, Health, Link } from "../types/graph";

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

// Official documentation per kind: kubernetes.io for built-ins, project docs
// for well-known operators. Frontend-only lookup — no snapshot data involved.
const K8S = "https://kubernetes.io/docs/concepts";
const KIND_DOCS: Record<string, string> = {
  Cluster: `${K8S}/overview/components/`,
  ControlPlane: `${K8S}/architecture/`,
  Node: `${K8S}/architecture/nodes/`,
  Namespace: `${K8S}/overview/working-with-objects/namespaces/`,
  Deployment: `${K8S}/workloads/controllers/deployment/`,
  ReplicaSet: `${K8S}/workloads/controllers/replicaset/`,
  StatefulSet: `${K8S}/workloads/controllers/statefulset/`,
  DaemonSet: `${K8S}/workloads/controllers/daemonset/`,
  Job: `${K8S}/workloads/controllers/job/`,
  CronJob: `${K8S}/workloads/controllers/cron-jobs/`,
  Pod: `${K8S}/workloads/pods/`,
  Service: `${K8S}/services-networking/service/`,
  Ingress: `${K8S}/services-networking/ingress/`,
  NetworkPolicy: `${K8S}/services-networking/network-policies/`,
  ConfigMap: `${K8S}/configuration/configmap/`,
  Secret: `${K8S}/configuration/secret/`,
  ServiceAccount: `${K8S}/security/service-accounts/`,
  PersistentVolumeClaim: `${K8S}/storage/persistent-volumes/`,
  PersistentVolume: `${K8S}/storage/persistent-volumes/`,
  StorageClass: `${K8S}/storage/storage-classes/`,
  StorageGroup: `${K8S}/storage/persistent-volumes/`,
  CRDGroup: `${K8S}/extend-kubernetes/api-extension/custom-resources/`,
  CustomResourceDefinition: `${K8S}/extend-kubernetes/api-extension/custom-resources/`,
  Kustomization: "https://fluxcd.io/flux/components/kustomize/kustomizations/",
  HelmRelease: "https://fluxcd.io/flux/components/helm/helmreleases/",
  GitRepository: "https://fluxcd.io/flux/components/source/gitrepositories/",
  OCIRepository: "https://fluxcd.io/flux/components/source/ocirepositories/",
  HelmRepository: "https://fluxcd.io/flux/components/source/helmrepositories/",
  Certificate: "https://cert-manager.io/docs/usage/certificate/",
  CertificateRequest: "https://cert-manager.io/docs/concepts/certificaterequest/",
  ClusterIssuer: "https://cert-manager.io/docs/configuration/",
  Issuer: "https://cert-manager.io/docs/configuration/",
  ServiceMonitor: "https://prometheus-operator.dev/docs/",
  PodMonitor: "https://prometheus-operator.dev/docs/",
  PrometheusRule: "https://prometheus-operator.dev/docs/",
  Prometheus: "https://prometheus-operator.dev/docs/",
  Alertmanager: "https://prometheus-operator.dev/docs/",
  IPAddressPool: "https://metallb.io/configuration/",
  L2Advertisement: "https://metallb.io/configuration/",
  BGPPeer: "https://metallb.io/configuration/",
  BGPAdvertisement: "https://metallb.io/configuration/",
  HelmChart: "https://docs.k3s.io/helm",
  HelmChartConfig: "https://docs.k3s.io/helm",
  Addon: "https://docs.k3s.io/installation/packaged-components",
};

// Control-plane components share the kind — the name picks the docs section.
const COMPONENT_DOCS: Record<string, string> = {
  "api-server": `${K8S}/architecture/#kube-apiserver`,
  etcd: `${K8S}/architecture/#etcd`,
  scheduler: `${K8S}/architecture/#kube-scheduler`,
  "controller-manager": `${K8S}/architecture/#kube-controller-manager`,
};

/** Official docs link for a node's kind, when one is known. */
export function kindDocsUrl(kind: string, name?: string): Link | null {
  if (kind === "Component" && name) {
    if (name.includes("kine")) {
      return { label: "k3s datastore docs", url: "https://docs.k3s.io/datastore" };
    }
    const url = COMPONENT_DOCS[name];
    return url ? { label: "Kubernetes docs", url } : null;
  }
  const url = KIND_DOCS[kind];
  if (!url) return null;
  const label = url.includes("fluxcd.io")
    ? "Flux docs"
    : url.includes("cert-manager.io")
      ? "cert-manager docs"
      : url.includes("prometheus-operator")
        ? "prometheus-operator docs"
        : url.includes("metallb.io")
          ? "MetalLB docs"
          : url.includes("k3s.io")
            ? "k3s docs"
            : "Kubernetes docs";
  return { label, url };
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

/**
 * What an edge means, in words a reader can act on rather than the API's own
 * vocabulary. "selects" is a label-selector match — true, and useless unless you
 * already knew that; a Service *routes to* a Pod and a NetworkPolicy *applies
 * to* one, which is the same edge saying two different things depending on
 * which end you are standing at.
 *
 * Keyed [outgoing, incoming] — read from the source's side, then the target's.
 */
const PHRASE: Record<string, [string, string]> = {
  mounts: ["mounts", "mounted by"],
  references: ["reads", "read by"],
  uses: ["runs as", "used by"],
  selects: ["selects", "selected by"],
  exposes: ["exposes", "exposed by"],
  binds: ["bound to", "binds"],
  "scheduled-on": ["runs on", "hosts"],
  "depends-on": ["depends on", "depended on by"],
  "managed-by": ["managed by", "manages"],
  "sourced-from": ["sourced from", "source of"],
  "instance-of": ["an instance of", "instantiated by"],
};

/** Overrides where the source's Kind changes what the edge actually means. */
const PHRASE_BY_SOURCE: Record<string, Record<string, [string, string]>> = {
  selects: {
    Service: ["routes to", "routed to by"],
    NetworkPolicy: ["applies to", "governed by"],
  },
};

/**
 * Plain-English name for an edge, from one end's point of view.
 * `direction` is "out" when reading from the source, "in" from the target.
 */
export function edgePhrase(
  kind: string,
  direction: "out" | "in",
  sourceKind?: string,
): string {
  const i = direction === "out" ? 0 : 1;
  const bySource = sourceKind
    ? PHRASE_BY_SOURCE[kind]?.[sourceKind]
    : undefined;
  const pair = bySource ?? PHRASE[kind];
  if (pair) return pair[i];
  return direction === "out" ? kind : `${kind} \u2190`;
}

/**
 * What a relationship actually means, for a tooltip. The phrase says what it
 * is; this says how kscope knows, which is the part that tells a reader whether
 * to trust it. Written direction-neutrally so one sentence reads correctly from
 * either end of the edge.
 */
const MEANING: Record<string, string> = {
  mounts: "Mounted into the container's filesystem as a volume.",
  references: "Read as environment variables, not mounted as a file.",
  uses: "The pod runs under this ServiceAccount.",
  selects: "Matched by a label selector.",
  exposes: "An Ingress rule routes requests to this Service.",
  binds: "The storage chain: claim, to volume, to class.",
  "scheduled-on": "The pod was running on this node.",
  "depends-on": "Part of the cluster's control-plane spine.",
  "managed-by": "A GitOps controller owns this; direct edits get reverted.",
  "sourced-from": "Manifests are pulled from this repository.",
  "instance-of": "The definition that declares this kind.",
};

/** Overrides where the source's Kind changes what the relationship means. */
const MEANING_BY_SOURCE: Record<string, Record<string, string>> = {
  selects: {
    Service: "This Service's selector matches the pod's labels, so traffic reaches it.",
    NetworkPolicy:
      "This policy matches the pod. An empty selector matches the whole namespace.",
  },
};

/** One sentence explaining an edge kind, or undefined if there is nothing to add. */
export function edgeMeaning(kind: string, sourceKind?: string): string | undefined {
  return (sourceKind ? MEANING_BY_SOURCE[kind]?.[sourceKind] : undefined) ?? MEANING[kind];
}

/** Label for an edge read from the target's side. */
export function incomingEdgeLabel(kind: string): string {
  return edgePhrase(kind, "in");
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
