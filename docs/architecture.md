# kscope Architecture

## Goal
Visualize Kubernetes resources as a connected graph — namespaces, deployments, CronJobs, CRDs, Crossplane XRDs/Compositions, and their relationships.

## Invocation model

kscope is **semi-dynamic**: the user picks a scope (currently just namespaces), triggers an invocation (UI button or CLI flag), and the server runs one discovery pass. The resulting snapshot is cached in memory and mirrored atomically to `./data/latest.json`. It sticks around — across page refreshes and server restarts — until the next invocation replaces it.

```
User picks scope  ──▶  POST /api/graph/refresh  ──▶  graph.Discover()  ──▶  store.Set()
                                                                              │
                       GET /api/graph/latest  ◀───  store.Get()  ◀────────────┘
```

Why server-side and not browser state: survives refresh, consistent across tabs, lets the backend CLI and the UI share the same snapshot store.

## Stack

### Backend (Go)
- Stdlib `net/http` with 1.22 method-prefixed patterns. No router dep.
- `client-go` (typed clientset) for discovery, loading the active kubeconfig
  with kubectl's resolution rules. The containment tree is derived from
  ownerReferences in a second pass (owners can be listed after their
  children); per-list failures are recorded in `Stats.Errors`, never fatal.
  The dynamic client joins in milestone 5 for generic CRD discovery.
- Handlers reach discovery through injectable function fields on `Server`, so
  handler tests run against fakes; discovery itself is tested with
  `client-go`'s fake clientset.
- Snapshot persistence: `sync.RWMutex`-guarded in-memory cache + atomic-rename file write.
- SPA served from the same process via `//go:embed`.

### Frontend
- Vite + React + TypeScript + Tailwind v4 (`@tailwindcss/vite`).
- `@xyflow/react` for the graph canvas.
- `@dagrejs/dagre` for auto-layout (top-to-bottom layered).
- TanStack Query for data fetching and mutation-invalidation.

### Serving model
- **Dev:** Vite dev server on `:5173` proxies `/api` and `/healthz` to the Go API on `:8080`.
- **Prod:** single Go binary; `web/dist/` is embedded via `web/embed.go` and served at `/`. The stdlib mux handles route precedence so `/api/*` and `/healthz` win over the SPA fallback.

### Desktop shell (Wails v2)

`cmd/kscope-desktop` wraps the same core in an OS webview. It is a second thin
entrypoint, not a fork: all logic stays in `internal/`.

The load-bearing detail is that **the desktop app opens no TCP port**. Wails'
`assetserver.Options` accepts a `Middleware`, which wraps the outermost handler
in both dev and production builds. `/api/*` and `/healthz` are claimed there and
routed straight into the existing `*http.ServeMux` (`server.Mux()`), so the
frontend keeps using plain same-origin `fetch` with no client changes and no
CORS story.

`Middleware` rather than `Handler` because the alternatives break in dev mode:
Wails' dev asset handler forwards every unmatched GET to Vite (so `/api/...`
would loop out to the dev server) and answers non-GET requests with a blanket
405 (so `POST /api/graph/refresh` would never arrive). Middleware runs ahead of
both. A corollary: the Vite proxy in `web/vite.config.ts` is only used by the
browser dev workflow — `wails dev` never touches it.

Single-instance and the focus handoff use Wails' own `SingleInstanceLock`
(dbus on Linux) rather than a hand-rolled socket: it is cross-platform, it
delivers the second process's argv, and the second process exits by itself.
Focus references are resolved **server-side** by scanning the snapshot's nodes
for a namespace+name match (`graph.ResolveNode`), with kind only as a
tie-breaker. Deliberately not by reconstructing kscope's node-ID format —
`web/src/lib/display.ts` already duplicates that format once, and a third copy
in an external caller would be worse. It also means callers need not know the
API group.

Two Wails behaviours worth remembering, both discovered the hard way:
`SetupSingleInstance` runs *after* `OnStartup` is dispatched, so a second
process logs its startup lines before handing off and exiting; and it exits
with **status 1** on the success path.

Wails' `build:tags` is set to `webkit2_41`; Fedora 44 ships only
`webkit2gtk-4.1`, and Wails v2 still defaults to the 4.0 pkg-config name.
`wails build` runs `go build` in the directory containing `wails.json`, so that
file lives in `cmd/kscope-desktop/` rather than at the repo root.

### K8s
- Remote k3s cluster via kubeconfig.
- No in-cluster deployment yet; dev mode connects from localhost.

## Packages

| Path | Purpose |
|---|---|
| `cmd/kscope` | HTTP server + CLI one-shot entrypoint; flags, startup load |
| `cmd/kscope-desktop` | Wails desktop entrypoint; serves the same mux in-process, no port |
| `internal/graph` | types, store (graph + manifests persistence), `discover.go` (listers, health, tree), `edges.go` (relationship inference), `manifest.go` (redacted YAML) |
| `internal/paths` | per-user data directory resolution (XDG on Linux) |
| `internal/server` | HTTP handlers + SPA fallback; `Mux()`/`IsAPIPath()` let a non-listening host reuse the routes |
| `web` | SPA + Go embed wrapper (`Dist embed.FS`), shared by both binaries |

## Data shape

See `docs/spec-v1.md` §3 for the full model. The essentials:

```go
type Scope struct {
    Namespaces []string `json:"namespaces"` // empty = every namespace
}
type Snapshot struct {
    Scope     Scope       `json:"scope"`
    Timestamp time.Time   `json:"timestamp"`
    Cluster   ClusterMeta `json:"cluster"` // context, server, version, distro
    Nodes     []Node      `json:"nodes"`   // ParentID carries the containment tree
    Edges     []Edge      `json:"edges"`   // reserved for cross-cutting relations (M2+)
    Stats     Stats       `json:"stats"`   // counts, duration, non-fatal errors
}
```

`Scope` is a struct, not a list, so adding dimensions (kinds, label selector, cluster-wide toggle) later is backwards-compatible on the wire. Containment lives in `Node.ParentID` (exactly one place in the tree per node); `Edges` stay reserved for cross-cutting relationships so the hierarchy is unambiguous.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| HTTP router | stdlib `net/http` 1.22 method patterns | No deps, enough |
| K8s client | `client-go` (planned) | Official, widely documented |
| Graph layout | Dagre | Layered DAG, cheap at current sizes |
| Snapshot keying | single global latest | Matches the "semi-dynamic" promise; avoids a views CRUD |
| Snapshot storage | JSON file, atomic rename | No DB; swappable for SQLite if/when history is needed |
| SPA serving | embedded via `go:embed` | Single-binary deploy; no CORS story |
| Desktop shell | Wails v2 over Electron/Tauri | Keeps the Go core as the app core; OS webview, ~10–15 MB binary. Tauri would mean rewriting the backend in Rust; Electron would mean a Go sidecar plus a bundled Chromium. v2 over v3-beta for maturity. |
| Desktop API transport | existing mux via `assetserver.Middleware` | Reuses the tested HTTP surface and its handler tests; zero frontend changes; no port open. Wails bindings are reserved for what HTTP genuinely cannot do (native dialogs, window control). |
| Data directory | XDG per-user path, `--data-dir` override | A menu-launched app has an arbitrary CWD, so `./data` would scatter snapshots |

## Open questions / future work
- Extra scope dimensions: resource kinds, label selectors, cluster-wide.
- Live updates (SSE/WebSocket) once snapshots have meaningful frequency.
- Handling very large clusters (pagination, virtual nodes, progressive rendering).
- Auth: kubeconfig only, or service account tokens for shared/deployed instances.
