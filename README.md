# kscope

A Kubernetes resource visualizer: picks a scope (namespaces for now), runs a discovery pass, and renders the result as an interactive map — a collapsible hierarchy tree (cluster → namespaces → workloads → pods) plus a focused graph of whatever you select, with health rollups, copyable `kubectl` retrieval commands, and shareable `?focus=` deep links. See `docs/spec-v1.md` for where this is headed.

The view is **semi-dynamic**: each invocation produces a snapshot that the server persists to disk and re-serves across page refreshes. The graph only changes when someone runs a new discovery.

## Stack

- **Backend:** Go + stdlib `net/http`; discovery via `client-go` against the active kubeconfig context.
- **Frontend:** React + TypeScript + `@xyflow/react` + Tailwind, built with Vite.
- **State:** snapshot cached in memory, mirrored to `./data/latest.json`.

## Endpoints

| Method + Path | What it does |
|---|---|
| `GET /healthz` | liveness check |
| `GET /api/namespaces` | namespace list for the scope picker |
| `GET /api/graph/latest` | last snapshot, or `204 No Content` if none yet |
| `POST /api/graph/refresh` | body `{ "namespaces": [...] }` (empty list = all namespaces) → runs discovery, updates the snapshot |
| `GET /api/node/manifest/{id...}` | one node's redacted YAML manifest (Secret values are never stored) |
| `GET /`, `GET /assets/*` | the embedded SPA (prod) |

## Running it

### Dev (two processes)

```bash
# 1. Start the Go API on :8080
go run ./cmd/kscope --port 8080

# 2. Start the Vite dev server on :5173 (proxies /api and /healthz to :8080)
cd web
npm install
npm run dev
```

Open http://localhost:5173.

### Prod (single binary, SPA embedded)

```bash
cd web && npm run build && cd ..
go build -o bin/kscope ./cmd/kscope
./bin/kscope --port 8080
```

Open http://localhost:8080.

### CLI-only invocation (no HTTP server)

Writes a snapshot to `./data/latest.json` and exits — useful for cron or ad-hoc runs:

```bash
go run ./cmd/kscope --discover-namespaces=default,monitoring
```

Subsequent `kscope` server starts will pick up that snapshot on boot.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--port` | `8080` | HTTP listen port |
| `--data-dir` | `./data` | directory holding `latest.json` |
| `--discover-namespaces` | `""` | one-shot mode: run discovery for these namespaces and exit |

## Status

Early development — building brick by brick, following `docs/spec-v1.md`:

- **Milestone 1 (done):** real `client-go` discovery of core workloads (Deployments, StatefulSets, DaemonSets, ReplicaSets, Pods, Jobs, CronJobs) with the containment tree derived from ownerReferences; per-resource health rollups; tree + focused-graph UI; `kubectl` retrieval hints.
- **Milestone 2 (done):** config/networking/storage discovery (ConfigMaps, Secrets, ServiceAccounts, Services, Ingresses, NetworkPolicies, PVC→PV→StorageClass) with inferred relationship edges (`mounts`, `references`, `uses`, `selects`, `exposes`, `binds`) drawn as a dashed overlay; redacted manifest capture (Secret values never touch disk, `--redact-extra` for site-specific fields) served per node; details panel with breadcrumb, clickable relationships and manifest viewer.
- **Milestone 3 (next):** infra layer — cluster Nodes + control-plane components and `depends-on` edges.
