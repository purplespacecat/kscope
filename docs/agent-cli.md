# Agent interface — kscope as a CLI an agent can call

Status: **implemented** (branch `feat/agent-cli`). Revised after adversarial
review (two blockers, seven majors — all addressed below).

## 1. Problem

kscope's snapshot is built for a human looking at a canvas. An AI agent working
on a cluster resource wants the same understanding — what is this thing, what
contains it, what is wired to it, is anything near it unhealthy — but has no way
to get it except the raw snapshot or a pile of `kubectl` calls.

Both are expensive. The reference snapshot (`gitlab-ci-exporter`, **one**
namespace, 34 nodes, 15 edges) is a 161 KB file. Most of that — 126 KB, 78% —
is 1,160 near-identical discovery error strings from a rate-limited pass, which
is a separate defect (§7). The graph itself, nodes plus edges, is **25.9 KB,
roughly 6.5k tokens**, and a single Pod node is ~600 bytes because it carries
`uid`, `apiVersion`, the full label map and a prepared `kubectl` string — all of
which the canvas uses and an agent does not. That cost scales with the number of
namespaces in scope. Reconstructing the same picture from `kubectl` costs a
dozen `get -o yaml` calls and more tokens again.

The information an agent actually needs is small: where the resource sits in the
containment tree, what it is connected to, and whether anything nearby is
unhealthy and why. kscope already computes the first three at discovery time;
the "why" it computes and then discards (§4.6).

## 2. Intent

Expose the existing graph as a **bounded projection** printed to stdout, so any
agent that can run a command can consume it.

- One resource's neighbourhood costs a few hundred bytes instead of thousands of
  tokens.
- The output is *bounded by construction*, not merely smaller — a large
  namespace cannot produce an unbounded response.
- Freshness is never implicit: every response states how old the snapshot is,
  which cluster it came from, and what scope it covers.
- An agent is never handed the wrong resource with a success exit code.
  Ambiguity is a distinct, recoverable outcome.
- kscope gains no new authority over the cluster. Discovery already performs
  read-only list/get calls; nothing here writes.

Non-goal: replacing `kubectl` for the agent. The map says what exists, how it
relates and what is wrong; detail is still fetched when detail is needed.

## 3. Approach

Subcommands on the existing `cmd/kscope` binary, which already owns the store
and discovery:

```
kscope map      <name> [--namespace] [--kind] [--depth] [--budget] [--data-dir]
kscope find     [--name-contains] [--kind] [--namespace] [--health] [--limit] [--data-dir]
kscope info     [--data-dir]
kscope manifest <name> [--namespace] [--kind] [--data-dir]
```

`--data-dir` is on every subcommand, defaulting to `paths.DataDir()` exactly as
the server does. It matters more than it looks: `scripts/dev.sh` points the dev
server at `./data`, so a developer who runs `dev.sh` and then `kscope map …` is
reading a different store unless told otherwise. `kscope info` prints the data
dir it read for exactly this reason.

### 3.1 Dispatch

Dispatch happens before flag parsing, on `os.Args[1]`:

| First argument | Result |
| --- | --- |
| a known subcommand | that subcommand runs |
| a flag (`--port`, `-h`, …) or nothing | the existing server / one-shot path runs, unchanged |
| any other word | `unknown command` on stderr, **exit 1** |

The third row exists because the alternative — falling through to the server —
means `kscope mpa foo` starts an HTTP listener that never returns, and an agent
with a timeout hangs. A bare `kscope --port 8080 --data-dir …` keeps its current
meaning, so `dev.sh` and the documented quickstart stay valid.

### 3.2 Argument parsing

Go's stdlib `flag` stops at the first positional argument, so a naive parse of
`kscope map gitlab-ci-exporter --kind deployment` silently drops `--kind`. In
the reference snapshot `gitlab-ci-exporter` names **four** nodes; the result
would be the Namespace's map, exit 0 — the exact failure §2 forbids.

Each subcommand therefore parses in two passes: parse, take `Args()[0]` as the
positional name, parse the remainder again. `name --flag` and `--flag name` are
equivalent, and a test asserts it (§8.4).

Flag sets use `flag.ContinueOnError`. `ExitOnError` exits **2** on an undefined
flag — the code §5.2 reserves for "out of scope, widen and retry" — so a typo
would send an agent off to re-run discovery. Parse errors map to exit 1.
`--help`/`-h` prints usage to **stdout** and exits 0: an agent runs `--help` to
learn the interface and reads stdout.

### 3.3 Layering

All logic stays in `internal/`, matching the rule in `docs/architecture.md`.

```
agent ⇄ shell ⇄ kscope map ⇄ internal/cli            (parse, exit codes, streams)
                           ⇄ internal/graph/view.go  (pure projection)
                           ⇄ graph.Store             (latest.json)
```

The projection is a pure function from a snapshot plus a focus to text. It knows
nothing about the CLI, is table-tested like `edges_test.go`, and could later
back a UI panel without change.

Rejected alternatives:

- **A separate `kscope-map` binary.** Leaves `cmd/kscope` untouched, but
  duplicates the data-dir and store wiring and adds a second artifact to
  package and version.
- **More flags on the existing binary** (`--map-resource …`). Smallest diff, but
  the flag surface already spans two modes; four more one-shot modes would make
  `--help` actively misleading — and `--help` is this design's discovery
  surface for an agent.
- **An agent-protocol server.** Designed first, then ruled out: the local
  security policy on this machine blocks that transport class outright.

The CLI adds **no new dependency**. Subcommands use stdlib `flag`, in keeping
with a repo that already declines a router dependency.

## 4. The projection

### 4.1 Neighbourhood, not subgraph

A focus node sits in two structures. The walk covers both, and `--depth` bounds
only one of them.

- **Ancestors** — the full `ParentID` chain to the cluster root, **always**,
  independent of depth. At most ~4 nodes (cluster → namespace → Deployment →
  ReplicaSet → Pod); it is what orients the agent and is never truncated.
- **Descendants** — containment children, `--depth` levels below the focus.
  Default **2**, cap **3**. Two is the canonical query: a Deployment's
  ReplicaSet *and* its Pods in one call. Beyond three the output approaches the
  whole snapshot, which is the cost this feature exists to avoid.
- **Edge peers** — **exactly one hop**, never chained, collected over the focus
  and every descendant in the set, **both directions**. Peers are leaves: their
  own children and edges are not walked.

Worked example, focus = a Deployment, depth 2: the set is {Deployment,
ReplicaSet, Pods}; edges are gathered from all of them, so a Service that
`selects` the Pods appears (incoming edge on a depth-2 node), but the
Kustomization that manages that Service does not (that would be a second hop).
Incoming edges matter more than outgoing for debugging: asked about a Secret, the
useful answer is which Pods mount it.

### 4.2 Fan-out is grouped

Children are grouped by kind under the same rule the canvas uses — **three or
more siblings of one kind form a group; Namespaces are never grouped** — and a
group renders as a plural header (`pluralize` from `focus.go`, not a second copy
of the frontend's table), a count, a health rollup, and up to **three** members,
unhealthy first.

Edges are rendered under the node that owns them. For a group, the members'
edges are rendered **once**, as a union deduplicated on (kind, peer), under the
group header — forty-seven Pods mounting one Secret is one line.

```
$ kscope map --kind deployment gitlab-ci-exporter

Cluster dev/ci1
└─ ns gitlab-ci-exporter
   └─ Deployment gitlab-ci-exporter        ✓ healthy    [flux: Kustomization/infra]
      kubectl --context dev/ci1 -n gitlab-ci-exporter get deployment gitlab-ci-exporter
      └─ ReplicaSet ...-84f9f6f845         ✓
         └─ Pods (47)                      ✓44 !2 ✗1
            ├─ ...-p2jx4                   ✗ CrashLoopBackOff
            ├─ ...-q8ln7                   ! ImagePullBackOff
            ├─ ...-rsbpk                   ✓
            ├─ … +44 more
            ├─ mounts → Secret gitlab-token
            ├─ references → ConfigMap exporter-cfg
            ├─ uses → ServiceAccount gitlab-ci-exporter
            └─ ← selects Service gitlab-ci-exporter

34 nodes / 15 edges in scope
snapshot 4h12m old · context=dev/ci1 · ns=[gitlab-ci-exporter] · data=~/.local/share/kscope
```

The group health rollup (`✓44 !2 ✗1`) is the highest-value line per byte in the
format: it answers "is anything wrong here" without listing anything. The
per-node reason (`CrashLoopBackOff`) is the second — see §4.6 for where it
comes from.

### 4.3 Budget

The projection takes a byte budget (`--budget`, in bytes, default **8192**) and
truncates to fit. Budget is bytes, not tokens: the tree glyphs (`✓ ├─ └─ →`)
are three to six bytes each, so 8192 bytes is nearer 2.5k tokens than 2k for
glyph-heavy output.

**Reserved, never truncated:** the ancestor chain, the focus line (with its
`kubectl` and GitOps lines), the footer, and the truncation marker itself. This
skeleton is bounded (~600 bytes), and `--budget` below **1024** is rejected with
exit 1 so the guarantee "output ≤ budget" is never quietly false.

That **1024** is the minimum for the projection itself; it is not the smallest
`--budget` value `kscope map` will actually accept. The footer and the counts
line (`N nodes / M edges in scope`) are printed after the projection but still
reserved out of the same budget, so the true floor for any given snapshot is
1024 plus their length. `kscope map` computes that floor up front, before
calling the projection, and rejects a `--budget` below it with exit 1, naming
the exact number for the snapshot at hand rather than the flat 1024.

**Truncation is a total order**, applied step by step until the output fits:

1. Group member examples shrink to zero — group headers and their rollups stay.
2. Edge lines are removed, deepest containment level first; within a level,
   peers on healthy nodes before peers on unhealthy ones; then by kind, then by
   name.
3. Descendant groups and ungrouped children are removed, deepest level first;
   within a level, healthy before unhealthy, then kind, then name.

Every removal is counted into one marker: `… truncated: N nodes, M edges
omitted`. The four sort keys do not always separate two rows — same level,
same kind, same name, different namespaces ties on all of them — and it is a
stable sort over the tree's own order that then decides. So the survivors are
deterministic and reproducible for a given input, not re-derivable from the
ordering rules alone; the test in §8.2 asserts which nodes they are, not
merely that the output is short enough.

### 4.4 What each node carries

**Health** (glyph: `✓ ! ✗ ?`), plus the **reason** when the node has one
(§4.6). Nothing else — no IDs, labels, UIDs or API versions.

The **focus node only** additionally carries its `kubectl` string and its Flux
`managed-by` tag, the two most probable next actions. The `kubectl` string is
informational output built by kscope from the object's own context, kind,
namespace and name; kscope never executes it and runs no shell at any point.

Node IDs are **not** printed. They are long (`apps/deployment/ns/name`) and
unnecessary: every subcommand takes name + namespace + kind. This is the same
reasoning `docs/architecture.md` gives for the k9s handoff — external callers
should not have to learn kscope's ID format.

### 4.5 Edge direction

Outgoing edges render as `mounts → Secret gitlab-token`. Incoming render as
`← selects Service gitlab-ci-exporter`, read as "that Service selects this".

Deliberately **not** inverse labels ("selected by"). `web/src/lib/display.ts`
already owns an inverse-label vocabulary; a second copy in Go would drift from
it, and the arrow carries the same information.

### 4.6 Prerequisite brick: `Node.Reason`

`graph.Node` carries a four-value `Health` and nothing else. `podHealth` in
`discover.go` already reads the container waiting reason — it literally tests
`w.Reason == "CrashLoopBackOff"` — and then collapses it to `HealthError`,
discarding the string. The projection cannot print what the model does not hold.

So this ships as a **separate, prior change**:

```go
// Reason is the short status word behind a non-healthy Health, when the
// source object has one — a container waiting reason such as
// CrashLoopBackOff or ImagePullBackOff. Empty for healthy nodes and for
// kinds whose health has no single reason.
Reason string `json:"reason,omitempty"`
```

Populated only where discovery already has the string in hand (Pods, initially).
`omitempty` means old snapshots decode unchanged and simply render glyph-only;
nothing else in the codebase reads the field until the UI chooses to.

## 5. The command contract

| Command | Purpose |
| --- | --- |
| `kscope info` | data dir, cluster context and version, scope, age, node/edge counts, per-kind counts, discovery errors as a **count plus the five most frequent distinct messages** (ties by first occurrence) — never the raw list (§1: 1,160 entries) |
| `kscope find` | one line per match (kind, name, namespace, health), plus the total. With no filters it lists the whole snapshot up to `--limit` (default 50), a cheap inventory. `--kind` accepts a Kind or its plural, as `ResolveNode` does. |
| `kscope map` | the tree of §4.2 |
| `kscope manifest` | redacted YAML from the existing sidecar store. **The one unbounded command**: it has no `--budget` and prints the stored YAML whole — a ConfigMap holding an embedded file can be megabytes. Truncating it is not an option (a cut-off manifest is still parseable YAML that reads as complete, which is exactly the "wrong answer with a success code" §2 rules out), so the bound is the caller's to apply |

### 5.1 Refresh reuses the one-shot path — and the one-shot path grows

Refresh is not a new subcommand. `kscope --discover-namespaces=a,b` already runs
one discovery pass into the store and exits, and the desktop app reads the same
store. But as it stands that path is **unsafe to suggest to an agent**: it has
no `--context`, so it discovers whatever kubeconfig's current-context is and can
overwrite the desktop's snapshot with a **different cluster**; it resets
`--include-infra`/`--include-crds` to `true` regardless of the previous scope;
it runs with no timeout; and it rejects an empty namespace list, so an
all-namespaces snapshot cannot be refreshed from the CLI at all.

One-shot mode therefore gains three flags:

| Flag | Meaning |
| --- | --- |
| `--context <name>` | kubeconfig context to discover against; sets `Scope.Context` |
| `--timeout <dur>` | bound on the pass, default `60s` (the HTTP path caps at 30s) |
| `--discover-all-namespaces` | every namespace; mutually exclusive with `--discover-namespaces` |

### 5.2 Staleness contract

Every read command ends with the same footer:

```
snapshot 4h12m old · context=dev/ci1 · ns=[gitlab-ci-exporter] · data=~/.local/share/kscope
```

`ns=[all]` renders for an all-namespaces scope.

A **miss** is answered with the scope that produced it and the exact command
that would widen it — echoing the snapshot's own context, data dir and include
flags, so the suggestion can never refresh a different cluster or silently
change scope:

```
No resource matching name=cube kind=deployment.

Snapshot scope is context=dev/ci1 ns=[gitlab-ci-exporter] (4h12m old);
'cube' may exist but be out of scope. To include it:
  kscope --context dev/ci1 --data-dir ~/.local/share/kscope \
         --discover-namespaces=gitlab-ci-exporter,cube \
         --include-infra=true --include-crds=true
```

That promise holds only because of what the hint refuses to interpolate. A
`--namespace` must be a DNS-1123 label, rejected with exit 1 where the flag is
read if it is not — `--namespace 'x --context prod'` would otherwise render a
second `--context`, and Go's `flag` takes the last occurrence, so following the
hint would discover a different cluster. The data dir, the context and the
namespace list are each emitted as one shell word, quoted when they contain
anything a shell would act on. And a snapshot that records no context at all
(pre-`ClusterMeta`, or hand-written) omits `--context` entirely, with a comment
saying the command will use the kubeconfig's current-context: a bare
`--context` would swallow the next flag as its value and leave a command that
starts the HTTP server and blocks.

An **ambiguous** match is answered with the candidates and no map. Today
`ResolveNode` returns the first match when the kind hint does not settle it;
that is a defensible landing spot for a human on a canvas and the worst possible
outcome for an agent. A new `ResolveCandidates(nodes, ref) []Node` in
`focus.go` exposes the full match set; `ResolveNode` keeps its behaviour for the
k9s handoff.

```
Ambiguous: 'gitlab-ci-exporter' matches 4 resources in the snapshot.
Add --kind to choose one:
  Namespace       gitlab-ci-exporter
  Deployment      gitlab-ci-exporter   -n gitlab-ci-exporter
  ServiceAccount  gitlab-ci-exporter   -n gitlab-ci-exporter
  HelmRelease     gitlab-ci-exporter   -n gitlab-ci-exporter
```

### 5.3 Exit codes

Prose is for the agent to read; the exit code is for it to branch on without
parsing.

| Code | Meaning |
| --- | --- |
| `0` | resolved and printed |
| `1` | error — bad arguments, unknown command, unreadable data dir, malformed snapshot, `--budget` below the effective minimum (1024 plus the footer and counts line for this snapshot — `kscope map` names the exact number) |
| `2` | **recoverable miss** — no match in scope, ambiguous match, or `manifest` on a synthetic node (which has none). The §5.2 message went to stderr. |
| `3` | **no snapshot** in the data dir — nothing has been discovered yet. The message names the one-shot command to run. |

`2` and `3` are distinct from `1` because each is a normal state with a known
next action, not a failure.

### 5.4 Streams

The projection goes to **stdout** and nothing else does. Diagnostics, miss and
ambiguity messages, and warnings go to **stderr**, so an agent capturing stdout
gets the map alone and a shell pipeline stays clean. `--help` is the one
exception: usage goes to stdout, because that is where an agent will look for it.

### 5.5 Cross-process freshness

The desktop app and the CLI share one store. Each CLI invocation is a fresh
process that loads from disk, so unlike a long-lived server it cannot serve a
snapshot the UI has already replaced. `Store.Load()` is already idempotent and
reads from disk; `internal/graph/store.go` needs no change.

One documented gap: `Store.Set` renames `latest.json` and then `manifests.json`
as two separate atomic writes, not one. A `kscope manifest` landing in the
millisecond between them can see the new graph with the old sidecar and report a
miss for a brand-new node. Re-running is the fix; it is not worth a lock.

## 6. Agent integration

There is no tool schema in this design, so an agent learns the interface two
ways:

1. `kscope --help` and `kscope <cmd> --help` describe the subcommands and flags.
2. A short block pasted into the `CLAUDE.md` of any repo where the agent should
   reach for it:

```markdown
## Cluster map
`kscope map <name> [--namespace ns] [--kind k]` prints a compact map of a
resource: its place in the containment tree, what it is wired to, and health
with reasons. Prefer it over `kubectl get -o yaml` when the question is "what
is this connected to" or "what is unhealthy near this". Flags may come before
or after the name. Exit 2 means out of scope or ambiguous — read stderr, it
names the fix. `kscope find --name-contains <frag>` locates a resource first;
`kscope info` shows what scope is loaded, how stale it is, and which data dir
it read.
```

Packaging changes accordingly: `packaging/nfpm.yaml` currently ships only
`kscope-desktop`, so the `kscope` binary is installed nowhere. An agent cannot
invoke what is not on `PATH`, so the package gains a second `contents` entry and
`scripts/package.sh` a `go build ./cmd/kscope` — placed **after** `wails build`,
which is what populates `web/dist`; built before it, the CLI would embed an
empty SPA and its server mode would serve nothing.

## 7. Non-goals

- A `--json` output mode. The text form is the denser one and the whole point;
  a second format is surface without a consumer until something needs to parse
  rather than read.
- Any write path to the cluster.
- Watching, streaming, or long-running modes. One invocation, one answer.
- Splitting the distro package so the CLI installs without the webview
  dependencies. It matters only if someone wants the CLI on a headless box.
- Deduplicating `Stats.Errors` at discovery time. The reference snapshot stores
  1,160 near-identical rate-limit errors; `kscope info` summarises them rather
  than fixing the source. That fix belongs to discovery and gets its own change.

## 8. Testing

1. **Projection** — table tests over Go-built snapshot fixtures, in the style of
   `edges_test.go`: ancestor chain always present; depth 1/2/3 membership for
   both containment and edges, including that a second edge hop is *not* taken;
   both edge directions; grouping threshold (two siblings ungrouped, three
   grouped, Namespaces never); health rollups; union-deduplicated group edges.
2. **Budget** — a fixture large enough to breach the cap asserts that output is
   within budget, that the marker is present with correct counts, that no node
   line is half-written, and — because §4.3 is a total order — **exactly which
   nodes survive**. Sabotage-verified: removing the cap, and separately swapping
   two steps of the order, must each fail this test. A second case asserts
   `--budget 1023` is exit 1.
3. **Resolution** — `ResolveCandidates` returns all matches; the CLI maps one
   candidate to a map, several to exit 2 plus the candidate list, none to exit 2
   plus the miss message with the snapshot's exact context/data-dir/flags echoed.
4. **Command layer** — each subcommand is a function taking an `io.Writer` pair
   and returning an exit code, so tests assert stdout, stderr and the code
   without spawning a process. Cases: `name --flag` ≡ `--flag name`; unknown
   flag is exit 1 not 2; unknown subcommand is exit 1 and does **not** reach the
   server path; `--help` is stdout + exit 0; empty store is exit 3; synthetic
   node `manifest` is exit 2; `info` on the reference-shaped fixture prints a
   five-line error summary, not 1,160 lines.
5. **Reason brick** — `discover_test.go` gains a Pod with a waiting reason and
   asserts `Node.Reason`; a healthy Pod asserts it empty.
6. **No test contacts a cluster.** Every test builds its snapshot fixture in
   Go and writes it to a temp data dir.

## 9. Files touched

Two changes, in order.

**Brick 1 — `Node.Reason`**

| File | Change |
| --- | --- |
| `internal/graph/types.go` | `Reason string` on `Node`, `omitempty` |
| `internal/graph/discover.go` | `podHealth` returns the reason it already reads |
| `internal/graph/discover_test.go` | reason present / absent cases |

**Brick 2 — the CLI**

| File | Change |
| --- | --- |
| `internal/graph/view.go` | new — pure projection: `Neighbourhood()`, `View.Text()`, the §4.3 order |
| `internal/graph/view_test.go` | new — table tests incl. the budget survivors case |
| `internal/graph/focus.go` | `ResolveCandidates`; `ResolveNode` unchanged |
| `internal/graph/focus_test.go` | candidates cases |
| `internal/cli/commands.go` | new — `map`/`find`/`info`/`manifest`, two-pass parse, exit codes |
| `internal/cli/commands_test.go` | new — stdout/stderr/exit-code assertions |
| `cmd/kscope/main.go` | dispatch table (§3.1); `--context`, `--timeout`, `--discover-all-namespaces` on one-shot mode |
| `packaging/nfpm.yaml` | one `contents` entry for `/usr/bin/kscope` |
| `scripts/package.sh` | `go build ./cmd/kscope` after `wails build`, before nfpm |
| `README.md` | subcommand usage, new one-shot flags, the `CLAUDE.md` snippet of §6 |
| `CLAUDE.md` | `cmd/kscope` has subcommands; correct the stale claim that the store writes `./data/latest.json` (it writes to `paths.DataDir()`) |

The release workflow needs no change: it already globs `dist/*.deb` and
`dist/*.rpm`.
