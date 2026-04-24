# kscope

- Build brick by brick — ask before adding new files or large code blocks
- User is learning Go + k8s — briefly explain non-obvious decisions
- Module: `github.com/purplespacecat/kscope`
- K8s: remote k3s cluster via kubeconfig
- Design docs and ADRs go in `/docs`
- Frontend lives in `/web` (Vite + React + TS + Tailwind v4 + @xyflow/react). Build output at `web/dist/` is embedded into the Go binary via `web/embed.go`.
- Snapshot persistence: `internal/graph/store.go` writes `./data/latest.json` atomically; the store is the single source of truth for the UI.
