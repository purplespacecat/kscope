# kscope

A Kubernetes resource visualizer: picks a scope (namespaces for now), runs a discovery pass, and renders the result as an interactive graph — namespaces, deployments, CronJobs, CRDs, Crossplane compositions, and the relationships between them.

The view is **semi-dynamic**: each invocation produces a snapshot that the server persists to disk and re-serves across page refreshes. The graph only changes when someone runs a new discovery.

## Stack

- **Backend:** Go + stdlib `net/http` (discovery is `client-go` — currently stubbed).
- **Frontend:** React + TypeScript + `@xyflow/react` + Tailwind, built with Vite.
- **State:** snapshot cached in memory, mirrored to `./data/latest.json`.

## Endpoints

| Method + Path | What it does |
|---|---|
| `GET /healthz` | liveness check |
| `GET /api/namespaces` | namespace list for the scope picker |
| `GET /api/graph/latest` | last snapshot, or `204 No Content` if none yet |
| `POST /api/graph/refresh` | body `{ "namespaces": [...] }` → runs discovery, updates the snapshot |
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

Early development — building brick by brick. Discovery is a stub; next step is wiring `client-go` behind the `Discover` function.
