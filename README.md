# kscope

A Kubernetes resource visualizer that renders your cluster as an interactive graph — namespaces, deployments, CronJobs, CRDs, Crossplane compositions, and their relationships, all in one view.

## What it does

- Connects to your k8s cluster via kubeconfig
- Discovers all resources: core types, CRDs, Crossplane XRDs/Compositions
- Exposes a graph API consumed by a React Flow frontend (in progress)

## Stack

- **Backend:** Go + `client-go`
- **Frontend:** React + TypeScript + React Flow _(planned)_

## Running locally

```bash
go run ./cmd/kscope --port 8080
curl localhost:8080/healthz
```

## Status

Early development — building brick by brick.
