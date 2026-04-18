# kscope Architecture

## Goal
Visualize Kubernetes resources as a connected graph — namespaces, deployments, CronJobs, CRDs, Crossplane XRDs/Compositions, and their relationships.

## Stack

### Backend (Go)
- `client-go` — official k8s Go client for listing/watching resources
- `controller-runtime` — helpers for CRD discovery
- HTTP API served over `net/http` (stdlib, no framework)
- WebSocket or SSE for live updates (future)

### Frontend (planned)
- React + TypeScript
- React Flow (`@xyflow/react`) for the graph canvas
- Tailwind CSS

### K8s
- Remote k3s cluster via kubeconfig
- No in-cluster deployment yet — dev mode connects from localhost

## Resource Graph Concept

```
Namespace
  ├── Deployment → ReplicaSet → Pod
  ├── CronJob
  ├── Service
  └── [CRDs attached to namespace]
       └── Crossplane XRD → Composition → ManagedResource
```

## Decisions

| Decision | Choice | Why |
|---|---|---|
| HTTP router | stdlib `net/http` 1.22 method patterns | No deps, good enough |
| K8s client | client-go | Official, widely documented |
| Graph layout (future) | Dagre | Handles directed acyclic graphs cleanly |

## Open Questions
- How to handle very large clusters (pagination, virtual nodes)?
- Auth: kubeconfig only, or support service account tokens?
