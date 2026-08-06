# kscope

A Kubernetes resource visualizer: picks a scope (namespaces for now), runs a discovery pass, and renders the result as an interactive map — a collapsible hierarchy tree (cluster → namespaces → workloads → pods) plus a focused graph of whatever you select, with health rollups, copyable `kubectl` retrieval commands, and shareable `?focus=` deep links. See `docs/spec-v1.md` for where this is headed.

The view is **semi-dynamic**: each invocation produces a snapshot that the server persists to disk and re-serves across page refreshes. The graph only changes when someone runs a new discovery.

## Stack

- **Backend:** Go + stdlib `net/http`; discovery via `client-go` against the active kubeconfig context.
- **Frontend:** React + TypeScript + `@xyflow/react` + Tailwind, built with Vite.
- **Desktop shell:** [Wails v2](https://wails.io) — an OS webview around the same Go core, no bundled browser runtime.
- **State:** snapshot cached in memory, mirrored to `latest.json` in the data dir.

There are two binaries over one shared core in `internal/`:

| Binary | What it is |
|---|---|
| `cmd/kscope` | the original HTTP server + one-shot CLI; opens a port, you visit a URL |
| `cmd/kscope-desktop` | the native app; **opens no port** — the same `http.ServeMux` is served in-process through Wails' asset server |

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

### Desktop app

Requires the Wails CLI plus a webview toolchain. On Fedora only `webkit2gtk-4.1`
is packaged (4.0 is gone), which is why `wails.json` sets `build:tags` to
`webkit2_41` — without that tag the build fails looking for 4.0.

```bash
sudo dnf install -y gtk3-devel webkit2gtk4.1-devel
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# wails runs `go build` in the directory holding wails.json, so it lives
# next to the desktop main package rather than at the repo root.
cd cmd/kscope-desktop
wails dev     # hot-reloading dev window
wails build   # → cmd/kscope-desktop/build/bin/kscope-desktop
```

The frontend talks to the backend over plain same-origin `fetch`, exactly as it
does in the browser. An `assetserver.Middleware` claims `/api/*` and `/healthz`
before Wails' own asset handling — necessary because in dev mode Wails forwards
unmatched GETs to Vite and answers non-GET requests with 405.

### Dev (two processes, browser)

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

Writes a snapshot to the data dir and exits — useful for cron or ad-hoc runs:

```bash
go run ./cmd/kscope --discover-namespaces=default,monitoring
```

Subsequent `kscope` server starts will pick up that snapshot on boot.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--port` | `8080` | HTTP listen port |
| `--data-dir` | `$XDG_DATA_HOME/kscope` | directory holding `latest.json` + `manifests.json` |
| `--discover-namespaces` | `""` | one-shot mode: run discovery for these namespaces and exit |
| `--include-infra` | `true` | one-shot mode: include cluster nodes + control-plane |
| `--redact-extra` | `""` | extra dotted paths to redact in every manifest, e.g. `spec.password` |

`--data-dir` defaults to a per-user location (`$XDG_DATA_HOME/kscope`, i.e.
`~/.local/share/kscope`, and `~/Library/Application Support/kscope` on macOS)
rather than `./data`. A desktop app launched from a menu entry inherits an
arbitrary working directory, so a relative default would scatter snapshots
wherever it happened to start. `scripts/dev.sh` still passes `--data-dir ./data`,
so the repo-local workflow is unchanged. `cmd/kscope-desktop` accepts
`--data-dir` and `--redact-extra` only; the rest are server/CLI flags.

## Status

Early development — building brick by brick, following `docs/spec-v1.md`:

- **Milestone 1 (done):** real `client-go` discovery of core workloads (Deployments, StatefulSets, DaemonSets, ReplicaSets, Pods, Jobs, CronJobs) with the containment tree derived from ownerReferences; per-resource health rollups; tree + focused-graph UI; `kubectl` retrieval hints.
- **Milestone 2 (done):** config/networking/storage discovery (ConfigMaps, Secrets, ServiceAccounts, Services, Ingresses, NetworkPolicies, PVC→PV→StorageClass) with inferred relationship edges (`mounts`, `references`, `uses`, `selects`, `exposes`, `binds`) drawn as a dashed overlay; redacted manifest capture (Secret values never touch disk, `--redact-extra` for site-specific fields) served per node; details panel with breadcrumb, clickable relationships and manifest viewer.
- **Milestone 3 (done):** infra layer — cluster Nodes (condition-based health incl. pressure/cordon), a logical control-plane (synthetic components on k3s, static-pod health mirroring on kubeadm), the `depends-on` spine (node → api-server → datastore), `scheduled-on` pod→node edges, and the `IncludeInfra` scope toggle.
- **Milestone 4 (done):** Flux GitOps — toolkit CRs (Kustomizations, HelmReleases, Git/OCI/Helm repositories) discovered via the dynamic client with versions resolved through the discovery API; every Flux-applied resource gets a `GitOpsRef` (via ownership labels) + `managed-by`/`sourced-from` edges; Kustomization sources resolve to sha-pinned GitHub/GitLab deep links; the details panel shows a GitOps card with "Open in Git".
- **Milestone 5 (done):** generic CRD discovery — every CRD's in-scope instances become nodes with `instance-of` edges back to their definition (grouped under a synthetic "custom resources" node; instance-less CRDs pruned; cluster-scoped instances live under their definition); Ready/Established condition health; `IncludeCRDs` toggle.

**v1 is complete.** See `docs/spec-v1.md` §8 for per-milestone notes, and the "later" list there for what's next (multi-cluster, live updates, large-cluster rendering).
