# kscope

- Build brick by brick — ask before adding new files or large code blocks
- User is learning Go + k8s — briefly explain non-obvious decisions
- Module: `github.com/purplespacecat/kscope`
- K8s: remote k3s cluster via kubeconfig
- **Design docs, specs and plans are NOT committed here** — this repo has no `/docs`. They
  live in the user's vault (`~/Documents/repo/docs`): specs in `specs/`, plans in `plans/`,
  architecture in `areas/kscope/`, all linked from the `kscope` area MOC. Write new ones
  there. A bare `(spec §N)` in a code comment refers to the v1 spec
  (`specs/2026-07-05-kscope-spec-v1.md`).
- Frontend lives in `/web` (Vite + React + TS + Tailwind v4 + @xyflow/react). Build output at `web/dist/` is embedded into the Go binary via `web/embed.go`.
- Graph model: containment is one unambiguous tree (`Node.ParentID`, derived from
  ownerReferences in a second pass, since owners can list after their children). Everything
  cross-cutting is an `Edge`. Per-list discovery failures go to `Stats.Errors` and are never
  fatal. Secret values are redacted at capture, never retroactively — `--redact-extra` must
  be set before the pass that writes the snapshot.
- Wails desktop shell (`cmd/kscope-desktop`), learned the hard way and easy to re-break:
  it opens **no TCP port** — `/api/*` is claimed via `assetserver.Options.Middleware`, the
  only hook that runs in both dev and production (a `Handler` loses: dev forwards unmatched
  GETs to Vite and answers non-GET with 405). `SetupSingleInstance` runs *after* `OnStartup`
  and the second process exits with **status 1** on the success path, which is why the k9s
  plugin needs `background: true`. `wails.json` lives in `cmd/kscope-desktop/` because
  `wails build` runs `go build` from the directory holding it, and `build:tags` must be
  `webkit2_41` (Fedora ships only webkit2gtk-4.1).
- Snapshot persistence: `internal/graph/store.go` writes `latest.json` + `manifests.json` atomically under `paths.DataDir()` (`$XDG_DATA_HOME/kscope`, `--data-dir` overrides); the store is the single source of truth for the UI and the CLI.
- `cmd/kscope` has subcommands (`map`, `find`, `info`, `manifest`) for agents — `internal/cli`,
  spec in the vault (`specs/2026-09-16-kscope-agent-cli-spec.md`). A leading flag still means
  "run the server"; a leading word always goes to `cli.Run`, so an unknown command exits 1
  rather than starting a listener. Exit codes are contract: `0` printed, `1` error,
  `2` recoverable (out of scope **or** ambiguous), `3` no snapshot. `find` exits 2 only
  when an *identity* filter (`--name-contains`/`--kind`/`--namespace`) matches nothing;
  a `--health` filter matching nothing is a result, not a miss, and exits 0. Projection on stdout,
  everything else on stderr; `--help` is the one exception and goes to stdout.
- `web/src/lib/tree.ts` is the **single source of visibility** for the graph canvas:
  `visibleTree(nodes, {rootId, expanded, expandedGroups})` decides what is on screen, and
  `GraphCanvas` only lays out what it is handed. Expansion is written **only** by user
  gestures — never derived from selection, which is what used to collapse things nobody
  asked to collapse. Solo mode ("one branch at a time") is an action modifier applied in
  `toggleExpand`, never read by `visibleTree`; if it ever leaks into the derivation,
  turning it off would resurrect branches. Spec in the vault
  (`specs/2026-09-17-kscope-org-chart-view.md`).
- The k9s handoff **discovers what it cannot find**. `cmd/kscope-desktop/focus.go` resolves
  against the snapshot and, on a miss, emits the `graph.Scope` that would contain the
  resource; the frontend runs that pass and then resolves the id through
  `GET /api/focus/resolve`, so `graph.ResolveNode` stays the only implementation of "this
  resource". The policy is `discoveryScope`, deliberately **pure** so it is a table test
  rather than something you learn by pressing a key at a cluster: same cluster adds a
  namespace, a different cluster replaces, unmapped kinds and unknown namespaces refuse.
  `Scope.CRDKinds` narrows a custom-resource pass to the one kind being hunted — CRs are
  listed cluster-wide once per CRD and filtered in-process, so a namespace scope saves
  nothing and a full sweep costs ~90s.
- Anything that calls `wruntime.EventsEmit` is **untestable in Go**: `getEvents` calls
  `log.Fatalf` on a context without Wails' event plumbing, taking the test binary with it.
  Keep decision logic in pure functions (`focusResult`, `discoveryScope`) and let the thin
  shell emit.
- k9s substitution, measured against v0.50.6 with a pty probe (the house rule is that
  appearing to work is not proof): in an all-namespaces view `$NAMESPACE` carries the
  **selected row's** namespace, not the literal `all`, and `$COL-NAMESPACE` carries the same.
  kscope keeps the `all`/`*` sentinel handling anyway and takes `--focus-row-namespace` as a
  fallback, because that behaviour is k9s's to change and the cost of being wrong is a
  cluster-wide pass.
- `internal/graph/view.go` is a **pure** projection (snapshot + focus → text): no I/O, no
  clock, no flags. Its byte budget is a hard cap — if you change what `emit` writes, change
  what `measure` prices, or the guarantee silently breaks.
- `--include-crds` is not namespace-scoped: namespaced CRs honour `--discover-namespaces`,
  cluster-scoped ones are always included. On a Crossplane/Kyverno cluster that is most of
  the snapshot (one namespace of `deploy/infra` → 11,242 nodes, 12 MB, ~90s).
