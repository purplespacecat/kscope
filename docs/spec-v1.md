# kscope v1 Spec — Interactive cluster map

Status: draft • Supersedes the stub described in `README.md` / `architecture.md` for
the discovery + UI layers. The invocation model (semi-dynamic snapshot, single
global latest, atomic file persistence) is unchanged.

## 1. Goal

Turn kscope from a flat demo graph into an **interactive map you can use to
understand a real cluster**. You pick a scope, run one discovery pass, and get a
snapshot you can explore two ways at once:

1. **Hierarchy** — a collapsible tree: `cluster → {control-plane, nodes,
   namespaces} → workloads → pods → mounted config/storage`. This is the
   "click and expand" drill-down.
2. **Relationships** — for any selected node, a focused graph of its
   non-containment dependencies (a pod → the ConfigMaps/Secrets/PVCs it mounts,
   a Service → the pods it selects, a custom resource → its CRD, a workload →
   the Flux Kustomization that manages it, a node → the control plane).

Every resource is inspectable: redacted YAML inline, a copyable `kubectl`
command to fetch it live, and — when Flux manages it — a link to the Git source
and the managing Flux object.

### Non-goals for v1
- Multi-cluster (root is a single cluster; the model leaves room to grow).
- Live/streaming updates (still snapshot-based; refresh re-runs discovery).
- Editing/applying anything. kscope is **read-only**.
- Crossplane-specific composition modeling (CRDs are discovered generically).
- Metrics/usage (CPU/mem). Health is derived from status, not `metrics-server`.

## 2. Design decisions (locked)

| Decision | Choice | Why |
|---|---|---|
| GitOps tool | **Flux** | source/kustomize/helm toolkit CRs + label back-refs |
| Expansion data | **Full upfront snapshot**, client-side expand | instant, offline from `latest.json`, no live access at view time |
| UI paradigm | **Tree + linked detail graph** | clean at cluster scale; the tree *is* the map, the graph shows a node's neighborhood |
| Infra depth | **cluster → nodes → control-plane** | answers "understand my cluster"; control-plane synthesized logically on k3s |
| Resource scope | core workloads, networking, config, storage, generic CRDs | broad coverage |
| Secrets | **discovered but values redacted** everywhere | keys/refs are useful; values never touch disk or the UI |
| Manifests | **embedded redacted YAML + `kubectl` hint** | offline inspection + reproducible retrieval |
| Clusters | **single**, cluster as tree root | multi-cluster is a later phase |
| CRDs | **generic** (kind/instance edges) | one path covers Flux, Crossplane, anything |

## 3. Data model

The containment **tree** and the dependency **edges** are separated:

- Tree position is a single `ParentID` on each node (unambiguous parent → clean
  tree UI).
- Everything cross-cutting (references, mounts, selectors, GitOps, infra) is an
  `Edge`. Edges may cross branches of the tree freely.

```go
// internal/graph/types.go  (evolution of the current file)

type Node struct {
    ID         string            `json:"id"`         // stable: "<group>/<kind>/<ns>/<name>" lowercased
    Kind       string            `json:"kind"`
    Name       string            `json:"name"`
    Namespace  string            `json:"namespace,omitempty"`
    APIVersion string            `json:"apiVersion,omitempty"`
    UID        string            `json:"uid,omitempty"`
    ParentID   string            `json:"parentId,omitempty"` // "" only for the cluster root
    Labels     map[string]string `json:"labels,omitempty"`   // small, filtered
    Health     Health            `json:"health"`             // healthy|warning|error|unknown
    Synthetic  bool              `json:"synthetic,omitempty"`// logical node (e.g. k3s control-plane)

    Kubectl    string            `json:"kubectl,omitempty"`  // "kubectl -n x get cm y -o yaml"
    GitOps     *GitOpsRef        `json:"gitops,omitempty"`   // nil unless Flux-managed
    Links      []Link            `json:"links,omitempty"`    // external deep-links
    // Manifest is stored separately (see §3.1), not inline, to keep the node list light.
}

type Health string // "healthy" | "warning" | "error" | "unknown"

type Edge struct {
    ID     string   `json:"id"`
    Source string   `json:"source"`
    Target string   `json:"target"`
    Kind   EdgeKind `json:"kind"`
}

// EdgeKind is the *relationship* vocabulary (containment lives in ParentID).
type EdgeKind string
const (
    EdgeReferences EdgeKind = "references"  // env/envFrom → ConfigMap/Secret
    EdgeMounts     EdgeKind = "mounts"      // volume → ConfigMap/Secret/PVC
    EdgeSelects    EdgeKind = "selects"     // Service/NetworkPolicy → Pods
    EdgeExposes    EdgeKind = "exposes"     // Ingress → Service
    EdgeBinds      EdgeKind = "binds"       // PVC → PV → StorageClass
    EdgeUses       EdgeKind = "uses"        // Pod → ServiceAccount
    EdgeInstanceOf EdgeKind = "instance-of" // custom resource → its CRD
    EdgeScheduled  EdgeKind = "scheduled-on"// Pod → Node
    EdgeManagedBy  EdgeKind = "managed-by"  // resource → Flux Kustomization/HelmRelease
    EdgeSourcedFrom EdgeKind = "sourced-from"// Kustomization → GitRepository
    EdgeDependsOn  EdgeKind = "depends-on"  // Node → control-plane; component → component
)

type GitOpsRef struct {
    Tool       string `json:"tool"`                 // "flux"
    Kind       string `json:"kind"`                 // "Kustomization" | "HelmRelease"
    Name       string `json:"name"`
    Namespace  string `json:"namespace"`
    SourceRepo string `json:"sourceRepo,omitempty"` // git URL
    SourcePath string `json:"sourcePath,omitempty"`
    Revision   string `json:"revision,omitempty"`
    WebURL     string `json:"webURL,omitempty"`     // github/gitlab tree deep-link when resolvable
}

type Link struct {
    Label string `json:"label"`
    URL   string `json:"url"`
}

type Snapshot struct {
    Scope     Scope     `json:"scope"`
    Timestamp time.Time `json:"timestamp"`
    Cluster   ClusterMeta `json:"cluster"`
    Nodes     []Node    `json:"nodes"`
    Edges     []Edge    `json:"edges"`
    Stats     Stats     `json:"stats"`  // counts by kind, discovery duration, errors
}

type ClusterMeta struct {
    Context    string `json:"context"`    // kubeconfig context name
    Server     string `json:"server"`     // api server URL (host only in UI)
    Version    string `json:"version"`    // server GitVersion
    Distro     string `json:"distro,omitempty"` // "k3s" detected from version string
}
```

`Scope` gains optional dimensions (backwards-compatible — decoder tolerates
missing keys):

```go
type Scope struct {
    Namespaces   []string `json:"namespaces"`             // empty ⇒ all namespaces
    IncludeInfra bool     `json:"includeInfra,omitempty"` // nodes + control-plane
    IncludeCRDs  bool     `json:"includeCRDs,omitempty"`  // generic CRD discovery
}
```

### 3.1 Manifest storage & redaction

Manifests are the largest and most sensitive part. They are **not** inline on
`Node`. Instead the snapshot writes a sibling file and the UI fetches on demand:

- `./data/latest.json` — nodes/edges/meta (the map). Small, always loaded.
- `./data/manifests.json` — `map[nodeID]string` of redacted YAML.
- `GET /api/node/manifest/{id...}` serves one redacted manifest (trailing
  wildcard because node IDs contain slashes).

Redaction is applied **at discovery time, before anything is written to disk**:

1. `Secret` and any `*.data`/`*.stringData` on Secret-like kinds → replace every
   value with `"«redacted»"`, keep the keys. Never store or transmit the value.
2. Strip noise + leak vectors from every manifest:
   `metadata.managedFields`, the
   `kubectl.kubernetes.io/last-applied-configuration` annotation (it can embed a
   full Secret), and `metadata.annotations` matching a configurable deny-list.
3. ServiceAccount tokens / `*.token` fields → redacted.
4. A `--redact-extra` flag takes extra JSONPath-ish keys for site-specific
   fields (e.g. a CRD that stores credentials in `spec.password`).

> Known limitation (documented, not solved in v1): a non-Secret manifest can
> still embed a plaintext credential in an arbitrary `spec` field. The deny-list
> is the mitigation; kscope does not attempt full secret-scanning.

## 4. Discovery (`internal/graph/discover.go`)

Replaces the stub. Built on `client-go` + the **dynamic client** so one code
path handles built-ins and CRDs uniformly.

### 4.1 Pipeline
```
kubeconfig → rest.Config → {discovery, dynamic, typed} clients
  1. cluster meta      (Discovery.ServerVersion, current context/server)
  2. enumerate GVRs    (ServerPreferredResources, filter to list+get verbs)
  3. list resources    per selected namespace (+ cluster-scoped kinds once)
  4. build nodes       (redact, compute health, kubectl hint, ParentID)
  5. infer edges       (rule set §4.3)
  6. infra layer       (§4.4) if Scope.IncludeInfra
  7. gitops pass       (§4.5) if Flux CRDs present
  8. assemble Snapshot + manifests sidecar
```

Discovery must be **resilient**: a permission error or missing API group on one
GVR is recorded in `Stats.Errors` and skipped, never fatal. Partial maps are
expected on RBAC-limited clusters.

### 4.2 Containment tree (`ParentID`)
- Root: the cluster node (synthetic, `ParentID=""`).
- Namespaces: `ParentID = cluster`.
- Namespaced resources: `ParentID = namespace` **unless** they have an
  `ownerReference` to another in-scope object, in which case `ParentID = owner`.
  This makes `Deployment → ReplicaSet → Pod` nest naturally.
- Pods' mounted config/storage are **not** children — they're `mounts`/`binds`
  edges (a ConfigMap lives under its namespace, referenced by many pods).

### 4.3 Edge inference rules
| From | To | Signal | EdgeKind |
|---|---|---|---|
| Pod | ConfigMap/Secret | `volumes[].configMap/secret` | `mounts` |
| Pod | PVC | `volumes[].persistentVolumeClaim` | `mounts` |
| Pod | ConfigMap/Secret | `env[].valueFrom`, `envFrom[]` | `references` |
| Pod | ServiceAccount | `spec.serviceAccountName` | `uses` |
| Pod | Node | `spec.nodeName` | `scheduled-on` |
| Service | Pod | label selector match | `selects` |
| Ingress | Service | `rules[].http.paths[].backend` | `exposes` |
| NetworkPolicy | Pod | `podSelector` match | `selects` |
| PVC | PV | `spec.volumeName` / bound claim | `binds` |
| PV | StorageClass | `spec.storageClassName` | `binds` |
| Custom resource | CRD | GVK ↔ CRD `spec.names` | `instance-of` |

Selector matches are computed in-process against the discovered pod set (no
extra API calls). Edges only connect **in-scope** nodes; a dangling reference
(e.g. a mounted ConfigMap outside the scope) is surfaced as a badge on the
source node, not a broken edge.

### 4.4 Infra / control-plane (`IncludeInfra`)
- **Nodes**: real, from `core/v1 Node`. `ParentID = cluster`. Health from node
  conditions (`Ready`, pressure flags).
- **Control-plane**: `ParentID = cluster`, grouped under a synthetic
  "control-plane" node. Two detection modes:
  - *kubeadm-style*: real static pods in `kube-system`
    (`kube-apiserver-*`, `etcd-*`, `kube-scheduler-*`,
    `kube-controller-manager-*`) → real nodes.
  - *k3s* (detected via `ClusterMeta.Distro`): the control plane is one process,
    so we emit **synthetic** logical component nodes (`Synthetic=true`,
    rendered with a dashed/"logical" style) for api-server, etcd/kine,
    scheduler, controller-manager, plus CoreDNS/kubelet where discoverable.
- Edges: `Node --depends-on--> api-server`; `scheduler/controller-manager
  --depends-on--> api-server`; `api-server --depends-on--> etcd`. This gives the
  "cluster depends on api server" relationship you asked for **as a compact
  infra subgraph**, not a hairball edge from every workload.

### 4.5 Flux GitOps pass
- Detect toolkit CRDs; skip silently if Flux isn't installed.
- Discover: `GitRepository`/`OCIRepository`/`HelmRepository`
  (`source.toolkit.fluxcd.io`), `Kustomization` (`kustomize.toolkit.fluxcd.io`),
  `HelmRelease` (`helm.toolkit.fluxcd.io`). These become normal nodes.
- **Back-reference managed resources** via Flux's own labels on each object:
  `kustomize.toolkit.fluxcd.io/name` + `/namespace`, and
  `helm.toolkit.fluxcd.io/name` + `/namespace`. For each labeled resource, set
  `Node.GitOps` and add a `managed-by` edge to the Kustomization/HelmRelease.
- **Resolve the source**: `Kustomization.spec.sourceRef` → the GitRepository
  (`spec.url`, `spec.ref`, applied revision from `status`) + `spec.path`. Add a
  `sourced-from` edge. Build `WebURL` (github/gitlab `.../tree/<ref>/<path>`)
  when the host is recognizable; otherwise leave the raw git URL + path.
- Health of Flux objects from their `Ready` condition.

### 4.6 `kubectl` hint
Deterministic per node, e.g.
`kubectl --context <ctx> -n <ns> get <resource> <name> -o yaml`
(cluster-scoped kinds drop `-n`). Shown copyable in the detail panel next to the
embedded YAML — the "how to retrieve this" you asked for.

## 5. Frontend (`web/`)

Three-pane layout, all driven by the single snapshot (instant client-side
expand). Reuses `@xyflow/react` + TanStack Query already in the repo.

```
┌───────────────┬──────────────────────────┬───────────────┐
│  Scope +      │  Focused relationship     │  Detail panel │
│  Tree (left)  │  graph (center)           │  (right)      │
│               │                           │               │
│ ▸ cluster     │      [selected node]      │ kind/name     │
│  ▸ nodes      │      ╱    │    ╲           │ health        │
│  ▸ ctrl-plane │  [dep] [dep] [dep]        │ GitOps card   │
│  ▾ namespaces │                           │ YAML (redact) │
│    ▾ default  │  (1-hop, expand to 2-hop) │ kubectl copy  │
│      ▾ deploy │                           │ links         │
└───────────────┴──────────────────────────┴───────────────┘
```

### Left — hierarchy tree
- Built from `nodes` + `ParentID`. Collapsible; **virtualized** (react-window or
  xyflow-independent list) so large namespaces stay smooth.
- Health badge per row; kind icon; count of hidden children when collapsed.
- Text filter (name/kind/namespace) + kind toggles (workloads/config/net/…).
- Selecting a row drives the center graph and right panel; also expands its
  ancestry.

### Center — focused relationship graph
- For the selected node: render it + its `Edge` neighbors (1-hop default,
  "expand" button → 2-hop). Keeps the current Dagre layout but on a *subgraph*,
  which is what makes it readable at scale.
- Edge kind → color/label (mounts, selects, managed-by, depends-on, …).
- Synthetic infra nodes rendered dashed; Flux-managed nodes get a badge.
- Clicking a neighbor re-focuses (and syncs the tree selection).

### Right — detail panel
Replaces today's raw-JSON dump with:
- Header: kind, name, namespace, health.
- **GitOps card** (when `Node.GitOps`): tool, managing object, source repo
  (link), path, revision, "Open in Git" (`WebURL`), and a `kubectl` to inspect
  the Kustomization.
- **Manifest**: lazy `GET /api/node/{id}/manifest`, syntax-highlighted YAML,
  redaction clearly marked.
- **kubectl**: copyable retrieval command.
- **Links**: external deep-links.
- Breadcrumb of the containment ancestry.

### Keep in sync
`web/src/types/graph.ts` mirrors the Go types by hand (as noted in the file).
This spec grows both sides together.

## 6. API changes
| Method + Path | Change |
|---|---|
| `POST /api/graph/refresh` | body now `{namespaces, includeInfra, includeCRDs}` |
| `GET /api/graph/latest` | returns the richer `Snapshot` (no manifests) |
| `GET /api/node/manifest/{id...}` | **new** — redacted YAML for one node |
| `GET /api/namespaces` | unchanged (real client-go list) |

## 7. Security & operational notes
- Read-only client-go; no mutating verbs ever issued.
- Redaction happens before disk write (§3.1). `latest.json` and the manifests
  sidecar should be treated as sensitive-ish (topology + config keys) even
  though values are stripped — document that `./data` is git-ignored.
- RBAC-limited kubeconfigs produce partial maps by design; errors surface in
  `Stats`, never crash discovery.
- k3s single-node is the primary target; control-plane is logical there.

## 8. Milestones (brick by brick)

1. **Real discovery + tree** ✅ *(shipped)* — client-go wiring, `Node.ParentID`,
   ownerRef nesting, namespace/workload/pod tree, extended types + TS mirror,
   tree UI replacing the flat graph. *(Core workloads only.)* Also landed:
   subtree health rollups in the tree, a node-budgeted focus graph (ancestry
   spine + descendant layers, "+N" chips for what's cut), and `?focus=` deep
   links.
2. **References + detail graph** ✅ *(shipped)* — config/secret/storage/
   networking edges, redaction layer, manifest sidecar +
   `/api/node/manifest/{id...}`, relationship overlay on the focus graph, new
   detail panel (breadcrumb, clickable relationships, manifest + copy).
   Deviations from plan: dangling out-of-scope references are dropped
   silently (source-node badge still TODO); manifests render as plain
   monospace (syntax highlighting deferred).
3. **Infra layer** ✅ *(shipped)* — nodes + control-plane (real vs synthetic),
   `depends-on` edges, `IncludeInfra` scope toggle. Component health mirrors
   kubeadm static pods when present; on k3s components are synthetic and
   healthy-by-reachability. CoreDNS/kubelet synthetic duplicates were skipped
   (CoreDNS already appears as a real Deployment).
4. **Flux GitOps** — toolkit discovery, label back-refs, source resolution,
   `WebURL` deep-links, GitOps card.
5. **Generic CRDs** — dynamic CRD + instance discovery, `instance-of` edges,
   `IncludeCRDs` toggle. (Covers Crossplane, Flux CRs, anything.)

*Later (post-v1):* multi-cluster root, live updates (SSE), large-cluster
progressive rendering, optional secret-scanning of non-Secret manifests.

## 9. Open questions
- Manifest sidecar vs. one file per node under `./data/manifests/`? Sidecar is
  simplest; per-node files scale better if snapshots get huge. Start with
  sidecar.
- Health model: how much to infer for kinds without a clear status (e.g. a bare
  ConfigMap is always "healthy"/n-a)? Propose: only workloads, pods, nodes and
  Flux objects get real health; everything else `unknown`/n-a.
- Selector matching cost on large pod sets — precompute a label index once per
  discovery; revisit if it dominates runtime.
