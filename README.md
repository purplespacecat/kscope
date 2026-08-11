# kscope

**A desktop app that maps your Kubernetes cluster.** Pick a cluster and a set
of namespaces, run a discovery pass, and explore the result as an interactive
map: a collapsible hierarchy tree (cluster → namespaces → workloads → pods)
plus a focused relationship graph of whatever you select.

What the map shows:

- **Health rollups** — every resource gets a coarse health derived from its
  status; parents show the worst health of their collapsed children, so a red
  namespace tells you where to look before you expand it.
- **Relationships** — inferred edges like *mounts*, *selects*, *exposes*,
  *scheduled-on*, drawn over the containment tree: which Service selects which
  Pods, which Pod mounts which ConfigMap, the PVC → PV → StorageClass chain.
- **GitOps lineage** — Flux-managed resources link back to their Kustomization
  or HelmRelease and from there to a sha-pinned link into the source repo.
- **CRDs, generically** — custom resources appear with *instance-of* edges to
  their definitions, no per-operator modelling required.
- **Redacted manifests** — every node's YAML one click away; Secret values are
  redacted at capture time and never touch disk. Copy it, or save it through a
  native file dialog.
- **Infra layer** — cluster Nodes and a logical control-plane with a
  *depends-on* spine, k3s-aware.
- **k9s handoff** — press `Shift-G` on a resource in [k9s](https://k9scli.io)
  and kscope raises its window focused on that resource.

The view is **semi-dynamic**: each discovery produces a snapshot that persists
across restarts, and the map only changes when you run a new discovery. That
makes it a *map you study*, not a dashboard you stare at.

kscope is **read-only**. It lists and watches nothing mutating — the only
writes are its own snapshot files under your user data directory.

## Installation

### Packages (Linux)

Grab the `.rpm` or `.deb` from the
[latest release](https://github.com/purplespacecat/kscope/releases/latest):

```bash
# Fedora / RHEL
sudo dnf install ./kscope-<version>-1.x86_64.rpm

# Debian / Ubuntu
sudo apt install ./kscope_<version>_amd64.deb
```

This installs `kscope-desktop`, an application-launcher entry, and the icon.
The webview (`webkit2gtk4.1` / `libwebkit2gtk-4.1-0`) is a package dependency
and installs alongside it.

### From source

Requires Go, Node 22+, the [Wails](https://wails.io) CLI, and a webview
toolchain:

```bash
# Fedora                                # Debian/Ubuntu
sudo dnf install -y gtk3-devel \        sudo apt install -y libgtk-3-dev \
  webkit2gtk4.1-devel                     libwebkit2gtk-4.1-dev

go install github.com/wailsapp/wails/v2/cmd/wails@latest

git clone https://github.com/purplespacecat/kscope && cd kscope/cmd/kscope-desktop
wails build   # → cmd/kscope-desktop/build/bin/kscope-desktop
```

## Quickstart

1. **Launch kscope** from your application launcher (or run `kscope-desktop`).
   It reads the same kubeconfig as `kubectl` — `$KUBECONFIG`, then
   `~/.kube/config`.
2. **Pick a scope** in the left panel: if your kubeconfig has several contexts
   a cluster picker appears; below it, tick the namespaces you care about.
3. **Run discovery** (the button, or `Ctrl-R`). A few seconds later the map is
   there: tree on the left, graph in the middle, details on the right.
4. **Explore.** Click anything — in the tree or the graph — to focus it: the
   details panel shows health, relationships, labels, its manifest, a copyable
   `kubectl` command, and (for Flux-managed resources) an "Open in Git" link.
   Dashed group cards fold same-kind siblings; click to expand. Clicking *in
   the graph* keeps the clicked card exactly where it is on screen while the
   rest of the map reflows around it, so you never lose your place; picking a
   resource from the tree instead re-frames the view around it. `Ctrl-0`
   re-centers.
5. **Come back later.** The snapshot persists — closing and reopening kscope
   shows the same map until you run a new discovery.

### k9s integration

```bash
# kscope-desktop must be on $PATH (the packages put it there)
cp contrib/k9s/plugins.yaml ~/.config/k9s/plugins.yaml
```

If you already have a k9s plugins file, add the `kscope:` entry under your
existing `plugins:` key instead — appending the whole file would produce two
top-level `plugins:` keys and invalid YAML.

Then in k9s, put the cursor on any resource and press `Shift-G`: the kscope
window raises focused on that resource. If it isn't in the current snapshot
(different cluster, namespace out of scope), kscope says so in a banner instead
of silently doing nothing. kscope keeps a single window — a second launch hands
its arguments to the running instance and exits.

The same handoff works from any tool that can run a command:

```bash
kscope-desktop --focus-namespace cert-manager \
               --focus-kind deployments --focus-name cert-manager-webhook
```

`--focus-kind` accepts a Kind (`Deployment`) or a plural resource name
(`deployments`) and is only a tie-breaker; namespace+name usually suffices.

## Other ways to run it

The same core also ships as `cmd/kscope`, an HTTP server — useful where a
desktop session isn't available:

```bash
# Server mode: serves the UI and API on a port
go build -o bin/kscope ./cmd/kscope && ./bin/kscope --port 8080
# → http://localhost:8080

# One-shot CLI: run a discovery, write the snapshot, exit (cron-friendly)
go run ./cmd/kscope --discover-namespaces=default,monitoring
```

Both binaries share one snapshot store, so a cron'd CLI discovery shows up in
whichever UI you open next.

### API

| Method + Path | What it does |
|---|---|
| `GET /healthz` | liveness check |
| `GET /api/contexts` | kubeconfig contexts (local file read; contacts no cluster) |
| `GET /api/namespaces` | namespace list; optional `?context=` selects the cluster |
| `GET /api/graph/latest` | last snapshot, or `204 No Content` if none yet |
| `POST /api/graph/refresh` | body `{ "context": "...", "namespaces": [...] }` (empty list = all namespaces, absent context = current-context) → runs discovery |
| `GET /api/node/manifest/{id...}` | one node's redacted YAML manifest |

The desktop app serves the same API **without opening a port** — it runs
in-process behind the webview.

## Flags

| Flag | Default | Applies to | Meaning |
|---|---|---|---|
| `--data-dir` | `$XDG_DATA_HOME/kscope`¹ | both | directory holding `latest.json` + `manifests.json` |
| `--redact-extra` | `""` | both | extra dotted paths to redact in every manifest, e.g. `spec.password` |
| `--focus-context/-namespace/-kind/-name` | `""` | desktop | resource to focus on launch (what the k9s plugin passes) |
| `--port` | `8080` | server | HTTP listen port |
| `--discover-namespaces` | `""` | server | one-shot mode: run discovery for these namespaces and exit |
| `--include-infra` / `--include-crds` | `true` | server | one-shot mode: infra layer / custom resources |

¹ `~/.local/share/kscope` on Linux, `~/Library/Application Support/kscope` on
macOS. A desktop app launched from a menu has an arbitrary working directory,
so the default is per-user rather than `./data`.

## Security notes

- Discovery is read-only, using your kubeconfig's credentials — kscope sees
  exactly what you can see, nothing more.
- Secret **values** are redacted before anything is written to disk;
  `metadata.managedFields` and `last-applied-configuration` (which can embed
  Secret plaintext) are stripped too. `--redact-extra` covers site-specific
  fields in CRDs.
- The desktop app opens no TCP port. Server mode binds the port you give it
  with no auth — keep it on localhost or behind something that has auth.

## How it works

Go core (`client-go` discovery, edge inference, snapshot store) + React/xyflow
frontend, wrapped in a [Wails v2](https://wails.io) webview for the desktop —
one codebase, ~14 MB packages, no bundled browser runtime. Design details and
decisions: [`docs/architecture.md`](docs/architecture.md); the full v1 product
spec: [`docs/spec-v1.md`](docs/spec-v1.md).

## Development

```bash
# Browser dev loop (Go API :8080 + Vite :5173 with proxy)
./scripts/dev.sh

# Desktop dev loop (hot-reloading native window)
cd cmd/kscope-desktop && wails dev

# Tests
go test ./...                    # backend
npm --prefix web test            # frontend (vitest + Testing Library)
npm --prefix web run test:watch  # frontend, watch mode

# Packages (.rpm/.deb via nfpm; VERSION is mandatory — the script enforces it)
VERSION=1.2.3 ./scripts/package.sh    # → dist/
```

Tagging `v*` runs CI and attaches packages to a GitHub release
(`.github/workflows/build.yml`).

Notes that save you a debugging session:

- On Fedora only `webkit2gtk-4.1` is packaged, so all Wails commands need the
  `webkit2_41` build tag — `wails.json` sets it; plain `go build ./...` needs
  `-tags webkit2_41`.
- `wails.json` lives in `cmd/kscope-desktop/` because `wails build` runs
  `go build` in the directory holding it. Run wails commands from there.
- Frontend code must not import the generated `web/wailsjs/` directory (it
  only exists after a Wails build); reach the runtime via the injected
  `window.runtime`/`window.go` globals through `web/src/lib/desktop.ts`, which
  degrades to browser equivalents.
- A second `kscope-desktop` launch exits with **status 1 on success** — that's
  Wails signalling "handed off to the running instance".
