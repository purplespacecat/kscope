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
- `client-go` (planned) for real discovery. Today `internal/graph/discover.go` is a stub that fabricates a small graph per selected namespace so the end-to-end pipe works without cluster access.
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

### K8s
- Remote k3s cluster via kubeconfig.
- No in-cluster deployment yet; dev mode connects from localhost.

## Packages

| Path | Purpose |
|---|---|
| `cmd/kscope` | binary entrypoint; flags, startup load, CLI one-shot |
| `internal/graph` | `Scope`, `Snapshot`, store (persistence), `Discover` (stub) |
| `internal/server` | HTTP handlers + SPA fallback |
| `web` | SPA + Go embed wrapper (`Dist embed.FS`) |

## Data shape

```go
type Scope struct {
    Namespaces []string `json:"namespaces"`
}
type Snapshot struct {
    Scope     Scope     `json:"scope"`
    Timestamp time.Time `json:"timestamp"`
    Nodes     []Node    `json:"nodes"`
    Edges     []Edge    `json:"edges"`
}
```

`Scope` is a struct, not a list, so adding dimensions (kinds, label selector, cluster-wide toggle) later is backwards-compatible on the wire.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| HTTP router | stdlib `net/http` 1.22 method patterns | No deps, enough |
| K8s client | `client-go` (planned) | Official, widely documented |
| Graph layout | Dagre | Layered DAG, cheap at current sizes |
| Snapshot keying | single global latest | Matches the "semi-dynamic" promise; avoids a views CRUD |
| Snapshot storage | JSON file, atomic rename | No DB; swappable for SQLite if/when history is needed |
| SPA serving | embedded via `go:embed` | Single-binary deploy; no CORS story |

## Open questions / future work
- Real `client-go` discovery behind `graph.Discover`.
- Extra scope dimensions: resource kinds, label selectors, cluster-wide.
- Live updates (SSE/WebSocket) once snapshots have meaningful frequency.
- Handling very large clusters (pagination, virtual nodes, progressive rendering).
- Auth: kubeconfig only, or service account tokens for shared/deployed instances.
