# Agent interface — kscope as a CLI an agent can call

Status: **designed, not implemented**. This document is the spec; the
implementation plan follows separately.

## 1. Problem

kscope's snapshot is built for a human looking at a canvas. An AI agent working
on a cluster resource wants the same understanding — what is this thing, what
contains it, what is wired to it — but has no way to get it except the raw
snapshot or a pile of `kubectl` calls.

Both are expensive. A real snapshot of **one** namespace on the reference
cluster (`gitlab-ci-exporter`, 34 nodes, 15 edges) serialises to **161 KB, about
41k tokens**. A single Pod node is 619 bytes on its own, because it carries
`uid`, `apiVersion`, the full label map and a prepared `kubectl` string — all of
which the canvas uses and an agent does not. Reconstructing the same picture
from `kubectl` costs a dozen `get -o yaml` calls and more tokens again.

The information an agent actually needs is small: where the resource sits in the
containment tree, what it is connected to, and whether anything nearby is
unhealthy. kscope already computes all three at discovery time.

## 2. Intent

Expose the existing graph as a **bounded projection** printed to stdout, so any
agent that can run a command can consume it.

- One resource's neighbourhood costs a few hundred bytes instead of tens of
  thousands of tokens.
- The output is *bounded by construction*, not merely smaller — a large
  namespace cannot produce an unbounded response.
- Freshness is never implicit: every response states how old the snapshot is and
  what scope it covers.
- kscope gains no new authority over the cluster. Discovery already performs
  read-only list/get calls; nothing here writes.

Non-goal: replacing `kubectl` for the agent. The map says what exists and how it
relates; detail is still fetched when detail is needed.

## 3. Approach

Subcommands on the existing `cmd/kscope` binary, which already owns the store
and discovery:

```
kscope map      <name> [--namespace] [--kind] [--depth] [--budget]
kscope find     [--name-contains] [--kind] [--namespace] [--health] [--limit]
kscope info
kscope manifest <name> [--namespace] [--kind]
```

A bare invocation keeps its current meaning — `kscope --port 8080 --data-dir …`
runs the server — so `scripts/dev.sh` and the documented quickstart stay valid.
Subcommand dispatch happens before flag parsing: a first argument matching a
known subcommand selects it; anything else — a flag, or no arguments at all —
falls through to the existing server path unchanged.

All logic stays in `internal/`, matching the rule in `docs/architecture.md`. The
projection (`internal/graph/view.go`) is a pure function from a snapshot plus a
focus to text — it knows nothing about the CLI, is table-tested like
`edges_test.go`, and could later back a UI panel without change.

```
agent ⇄ shell ⇄ kscope map ⇄ internal/graph/view.go  (pure projection)
                           ⇄ graph.Store             (latest.json)
```

Rejected alternatives:

- **A separate `kscope-map` binary.** Leaves `cmd/kscope` untouched, but
  duplicates the data-dir and store wiring and adds a second artifact to
  package and version.
- **More flags on the existing binary** (`--map-resource …`). Smallest diff, but
  the flag surface already spans two modes; four more one-shot modes would make
  `--help` actively misleading — and `--help` is this design's entire discovery
  surface for an agent.
- **An agent-protocol server.** Designed first, then ruled out: the local
  security policy on this machine blocks that transport class outright.

The CLI adds **no new dependency**. Subcommands use stdlib `flag`, in keeping
with a repo that already declines a router dependency.

## 4. The projection

### 4.1 Neighbourhood, not subgraph

A focus node sits in two structures, so the walk covers both.

- **Ancestors** — the full `ParentID` chain to the cluster root, **always**,
  independent of depth. At most ~4 nodes (cluster → namespace → Deployment →
  ReplicaSet → Pod) and it is what orients the agent. Never truncated.
- **Descendants** — children, `--depth` levels down.
- **Edge peers** — cross-cutting edges within `--depth` hops, **both
  directions**. Incoming matters more than outgoing for debugging: asked about a
  Secret, the useful answer is which Pods mount it.

`--depth` defaults to **1** and is capped at **3**. Beyond 3 the output
approaches the whole snapshot, which is the cost this feature exists to avoid.

### 4.2 Fan-out is grouped

Children are grouped by kind — the same grouping the canvas uses — and rendered
as a count plus a health rollup plus a few examples:

```
$ kscope map gitlab-ci-exporter --kind deployment

ns gitlab-ci-exporter                      [flux: Kustomization/infra]
└─ Deployment gitlab-ci-exporter           ✓ healthy
   └─ ReplicaSet ...-84f9f6f845            ✓
      └─ Pods (47)                         ✓44 !2 ✗1
         ├─ ...-rsbpk                      ✓
         ├─ ...-p2jx4                      ✗ CrashLoopBackOff
         └─ … +45 more
         ├─ mounts → Secret gitlab-token
         ├─ references → ConfigMap exporter-cfg
         └─ uses → ServiceAccount gitlab-ci-exporter

34 nodes / 15 edges in scope
snapshot 4h12m old · context=deploy/infra · ns=[gitlab-ci-exporter]
```

The group health rollup (`✓44 !2 ✗1`) is the highest-value line per byte in the
format: it answers "is anything wrong here" without listing anything.

### 4.3 Budget

The projection takes a byte budget (`--budget`, in bytes, default **8192**,
roughly 2k tokens) and truncates deterministically: deepest first, then least relevant,
never mid-node, always leaving an explicit `… truncated: N nodes omitted`
marker.

This is what makes the output bounded rather than merely smaller. Without it a
`--depth 3` request on a busy namespace would cost an agent more context than
the `kubectl` output it replaced.

### 4.4 What each node carries

Only **health** (glyph, plus a reason when not healthy). The focus node
additionally carries its `kubectl` string and its Flux `managed-by` line — the
two most probable next actions. That `kubectl` string is informational output
built by kscope from the object's own kind, namespace and name; kscope itself
never executes it, and runs no shell at any point.

Node IDs are **not** printed. They are long (`apps/deployment/ns/name`) and
unnecessary: every subcommand takes name + namespace + kind and resolves through
the existing `graph.ResolveNode`. This is the same reasoning
`docs/architecture.md` gives for the k9s handoff — external callers should not
have to learn kscope's ID format.

### 4.5 Edge direction

Outgoing edges render as `mounts → Secret/foo`. Incoming render as
`← mounts Pod/bar`, read as "bar mounts this".

Deliberately **not** inverse labels ("mounted by"). `web/src/lib/display.ts`
already owns an inverse-label vocabulary; a second copy in Go would drift from
it, and the arrow carries the same information.

## 5. The command contract

| Command | Purpose |
| --- | --- |
| `kscope info` | cluster, scope, age, node/edge counts, per-kind counts, discovery errors |
| `kscope find` | one line per match, plus the total — an agent usually holds a name fragment, not an exact name. With no filters it lists the whole snapshot up to `--limit`, which makes it a cheap inventory. |
| `kscope map` | the tree of §4.2 |
| `kscope manifest` | redacted YAML from the existing sidecar store |

Refresh is **not** a new subcommand. `kscope --discover-namespaces=a,b
--include-infra --include-crds` already runs one discovery pass and exits; it is
documented here as the way to widen scope rather than reimplemented.

### 5.1 Staleness contract

Every read command ends with the same footer:

```
snapshot 4h12m old · context=deploy/infra · ns=[gitlab-ci-exporter]
```

A miss is answered with the scope that produced it and the command that would
fix it, because an agent told only "not found" retries blindly:

```
No resource matching name=cube kind=deployment.

Snapshot scope is ns=[gitlab-ci-exporter] (4h12m old) — 'cube' may exist
but be out of scope. To include it:
  kscope --discover-namespaces=gitlab-ci-exporter,cube
```

### 5.2 Exit codes

Prose is for the agent to read; the exit code is for it to branch on without
parsing.

| Code | Meaning |
| --- | --- |
| `0` | resolved and printed |
| `2` | no match in the current snapshot (the §5.1 message went to stderr) |
| `1` | error — unreadable data dir, malformed snapshot, bad arguments |

`2` is distinct from `1` precisely because "out of scope" is a normal,
recoverable state with a known next action, not a failure.

### 5.3 Streams

The projection goes to **stdout** and nothing else does. Diagnostics, the miss
message and warnings go to **stderr**, so an agent capturing stdout gets the map
alone and a shell pipeline stays clean.

### 5.4 Cross-process freshness

The desktop app and the CLI share one `latest.json`. `Store.Set` writes by
atomic rename, so a reader never sees a torn file, and each CLI invocation is a
fresh process that loads from disk — so unlike a long-lived server, the CLI
cannot serve a snapshot the UI has already replaced.

`Store.Load()` is already idempotent and reads from disk, so
`internal/graph/store.go` needs no change.

## 6. Agent integration

There is no tool schema in this design, so an agent learns the interface two
ways:

1. `kscope --help` and `kscope map --help` describe the subcommands and flags.
2. A short block pasted into the `CLAUDE.md` of any repo where the agent should
   reach for it:

```markdown
## Cluster map
`kscope map <name> [--namespace ns] [--kind k]` prints a compact map of a
resource: its place in the containment tree, what it is wired to, and health.
Prefer it over `kubectl get -o yaml` when the question is "what is this
connected to". `kscope find --name-contains <frag>` locates a resource first;
`kscope info` shows what scope is loaded and how stale it is.
```

Packaging changes accordingly: `packaging/nfpm.yaml` currently ships only
`kscope-desktop`, so the `kscope` binary is installed nowhere. An agent cannot
invoke what is not on `PATH`, so the package gains a second `contents` entry and
`scripts/package.sh` a `go build` for it.

## 7. Non-goals

- A `--json` output mode. The text form is the denser one and the whole point;
  a second format is surface without a consumer until something needs to parse
  rather than read.
- Any write path to the cluster.
- Watching, streaming, or long-running modes. One invocation, one answer.
- Splitting the distro package so the CLI installs without the webview
  dependencies. Noted, not solved — it matters only if someone wants the CLI on
  a headless box.

## 8. Testing

1. **Projection** — table tests over Go-built snapshot fixtures, in the style of
   `edges_test.go`: ancestor chain always present, depth 1 vs 2 membership, both
   edge directions, grouping and health rollups.
2. **Budget** — a fixture large enough to breach the cap asserts that output is
   within budget, that the truncation marker is present, and that no node line
   is half-written. Sabotage-verified: removing the cap must fail this test.
3. **Resolution** — reuses `graph.ResolveNode` and its existing tests; the new
   cases are the miss message and the ambiguous-kind hint.
4. **Command layer** — each subcommand is a function taking an `io.Writer` pair
   and returning an exit code, so tests assert stdout content, stderr content
   and the code without spawning a process. A dispatch test asserts that a bare
   invocation with `--port` still selects the server path.
5. **No test contacts a cluster.** Every test builds its snapshot fixture in
   Go and writes it to a temp data dir.

## 9. Files touched

| File | Change |
| --- | --- |
| `internal/graph/view.go` | new — pure projection: `Neighbourhood()`, `View.Text()` |
| `internal/graph/view_test.go` | new — table tests incl. the budget sabotage case |
| `internal/cli/commands.go` | new — `map`/`find`/`info`/`manifest`, each an exit-code-returning func |
| `internal/cli/commands_test.go` | new — stdout/stderr/exit-code assertions |
| `cmd/kscope/main.go` | subcommand dispatch ahead of the existing flag parsing |
| `packaging/nfpm.yaml` | one `contents` entry for `/usr/bin/kscope` |
| `scripts/package.sh` | one `go build` before nfpm runs |
| `README.md` | subcommand usage + the `CLAUDE.md` snippet of §6 |
| `CLAUDE.md` | note that `cmd/kscope` now has subcommands |

The release workflow needs no change: it already globs `dist/*.deb` and
`dist/*.rpm`.
