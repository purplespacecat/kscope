# kscope

- Build brick by brick — ask before adding new files or large code blocks
- User is learning Go + k8s — briefly explain non-obvious decisions
- Module: `github.com/purplespacecat/kscope`
- K8s: remote k3s cluster via kubeconfig
- **Design specs and implementation plans are NOT committed here.** They live in the user's
  docs vault (`~/Documents/repo/docs`): specs in `specs/`, plans in `plans/`, linked from the
  `kscope` area MOC. Write new ones there, not into this repo. `/docs` keeps only the
  narrow design notes already committed (architecture, focus anchoring, group expansion,
  namespace picker) — don't add to them.
- Frontend lives in `/web` (Vite + React + TS + Tailwind v4 + @xyflow/react). Build output at `web/dist/` is embedded into the Go binary via `web/embed.go`.
- Snapshot persistence: `internal/graph/store.go` writes `latest.json` + `manifests.json` atomically under `paths.DataDir()` (`$XDG_DATA_HOME/kscope`, `--data-dir` overrides); the store is the single source of truth for the UI and the CLI.
- `cmd/kscope` has subcommands (`map`, `find`, `info`, `manifest`) for agents — `internal/cli`,
  spec in the vault (`specs/2026-09-16-kscope-agent-cli-spec.md`). A leading flag still means
  "run the server"; a leading word always goes to `cli.Run`, so an unknown command exits 1
  rather than starting a listener. Exit codes are contract: `0` printed, `1` error,
  `2` recoverable (out of scope **or** ambiguous), `3` no snapshot. Projection on stdout,
  everything else on stderr; `--help` is the one exception and goes to stdout.
- `internal/graph/view.go` is a **pure** projection (snapshot + focus → text): no I/O, no
  clock, no flags. Its byte budget is a hard cap — if you change what `emit` writes, change
  what `measure` prices, or the guarantee silently breaks.
- `--include-crds` is not namespace-scoped: namespaced CRs honour `--discover-namespaces`,
  cluster-scoped ones are always included. On a Crossplane/Kyverno cluster that is most of
  the snapshot (one namespace of `deploy/infra` → 11,242 nodes, 12 MB, ~90s).
