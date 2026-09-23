import { useEffect, useMemo, useRef, useState } from "react";
import { Header } from "./components/Header";
import { ScopePanel } from "./components/ScopePanel";
import { TreePanel } from "./components/TreePanel";
import { GraphCanvas } from "./components/GraphCanvas";
import { DetailsPanel } from "./components/DetailsPanel";
import { useQueryClient } from "@tanstack/react-query";
import { useLatest } from "./hooks/useGraph";
import * as api from "./api/client";
import {
  DESKTOP_EVENTS,
  onDesktopData,
  type FocusRequest,
} from "./lib/desktop";
import type { GraphEdge, GraphNode, Scope } from "./types/graph";
import {
  controllerMarks,
  outlineColors,
  prune,
  relate,
  reveal,
  showParent,
  toggleExpand,
  toggleGroup,
  type TreeState,
  visibleTree,
} from "./lib/tree";

// Stable empty arrays so hooks downstream don't re-fire while loading.
const NO_NODES: GraphNode[] = [];
const NO_EDGES: GraphEdge[] = [];

export default function App() {
  const { data: snapshot, isLoading, error } = useLatest();
  const qc = useQueryClient();
  // Selection is stored as an id, seeded from ?focus= so any view is a
  // shareable URL; the node object is derived from the current snapshot, so
  // selection survives snapshot refreshes when the resource still exists.
  const [selectedId, setSelectedId] = useState<string | null>(
    () => new URLSearchParams(window.location.search).get("focus"),
  );

  // Expansion state for the graph. Written only by user gestures — a toggle, a
  // reveal, "show parent", or the one-time seed below. Deliberately NOT derived
  // from selection: that was what collapsed things nobody asked to collapse.
  const [tree, setTree] = useState<TreeState>(() => ({
    rootId: null,
    expanded: new Set<string>(),
    expandedGroups: new Set<string>(),
  }));
  // "One branch at a time": opt-in auto-collapse of siblings on expand.
  const [solo, setSolo] = useState(false);
  // Bumped by reveals from outside the canvas, to ask it to re-frame.
  const [revealTick, setRevealTick] = useState(0);
  const [spotlight, setSpotlight] = useState<{ id: string | null; tick: number }>({
    id: null,
    tick: 0,
  });

  // Panel visibility. Session-only by design: a fresh launch starts with
  // everything visible. Collapsing the details panel keeps the selection —
  // unlike its ✕, which deselects.
  const [leftOpen, setLeftOpen] = useState(true);
  const [rightOpen, setRightOpen] = useState(true);

  const nodes = snapshot?.nodes ?? NO_NODES;
  const allEdges = snapshot?.edges ?? NO_EDGES;
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  const selected = useMemo(
    () => nodes.find((n) => n.id === selectedId) ?? null,
    [nodes, selectedId],
  );

  // A new snapshot prunes rather than resets: re-running discovery over one
  // scope regenerates most ids identically, so throwing the open tree away
  // would cost the user their place for nothing.
  const prevNodes = useRef<GraphNode[]>(NO_NODES);
  useEffect(() => {
    if (nodes.length === 0) return;
    const prev = prevNodes.current;
    prevNodes.current = nodes;
    setTree((cur) => {
      if (prev.length > 0 && cur.rootId) return prune(prev, nodes, cur, null).state;
      // First snapshot: open the cluster root so the namespaces are showing — a
      // single collapsed card would be consistent and useless.
      const root = nodes.find((n) => !n.parentId)?.id ?? nodes[0].id;
      const seeded: TreeState = {
        rootId: root,
        expanded: new Set([root]),
        expandedGroups: new Set<string>(),
      };
      // ?focus= is written on *every* click, so it is a restored selection, not
      // a jump: reveal it inside the map rather than re-rooting on it. Rooting
      // here stranded a reload on whatever card was last clicked — a leaf meant
      // one card and an empty canvas. The k9s handoff is the thing that
      // re-roots, and it arrives over IPC (see the focus effect below).
      const focus = new URLSearchParams(window.location.search).get("focus");
      return focus && nodes.some((n) => n.id === focus)
        ? reveal(nodes, seeded, focus, false)
        : seeded;
    });
    setSelectedId((cur) => (cur && nodes.some((n) => n.id === cur) ? cur : null));
  }, [nodes]);

  const visible = useMemo(() => visibleTree(nodes, tree), [nodes, tree]);
  const relations = useMemo(
    () => relate(nodes, allEdges, visible, selectedId),
    [nodes, allEdges, visible, selectedId],
  );
  const outlines = useMemo(() => outlineColors(relations), [relations]);
  const marks = useMemo(() => controllerMarks(nodes, allEdges), [nodes, allEdges]);
  const childCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const n of nodes) {
      if (!n.parentId) continue;
      counts.set(n.parentId, (counts.get(n.parentId) ?? 0) + 1);
    }
    return counts;
  }, [nodes]);
  const rootParent = useMemo(() => {
    const root = tree.rootId ? byId.get(tree.rootId) : undefined;
    return root?.parentId ? byId.get(root.parentId) : undefined;
  }, [tree.rootId, byId]);

  const select = (n: GraphNode | null) => {
    setSelectedId(n?.id ?? null);
    const url = new URL(window.location.href);
    if (n) url.searchParams.set("focus", n.id);
    else url.searchParams.delete("focus");
    window.history.replaceState(null, "", url);
  };

  // Structure-changing gestures. Each one is an explicit click, which is the
  // only thing allowed to write expansion state.
  const toggleNode = (id: string) => setTree((cur) => toggleExpand(nodes, cur, id, solo));
  const toggleGroupCard = (gid: string) => setTree((cur) => toggleGroup(cur, gid));
  const climb = () => setTree((cur) => showParent(nodes, cur));
  // Picking from the sidebar tree names a node that may not be on screen, so it
  // reveals: selection alone never changes what is visible.
  // Taking the user to a related resource is a *reveal*, never a selection:
  // selecting it would swap the relationship panel over to that resource and
  // lose the context they were working through.
  const goToRelated = (id: string) => {
    setTree((cur) => reveal(nodes, cur, id, solo));
    setRevealTick((t) => t + 1);
    // Revealing can open several levels and re-frame the whole canvas, so say
    // which card the click was about. The tick makes a repeat click on the same
    // resource flag it again.
    setSpotlight((cur) => ({ id, tick: cur.tick + 1 }));
  };

  const selectAndReveal = (n: GraphNode | null) => {
    select(n);
    if (!n) return;
    setTree((cur) => reveal(nodes, cur, n.id, solo));
    // A reveal can open several levels at once, so the target could land
    // anywhere in a large layout. A sidebar pick has no on-screen origin to
    // preserve, so it re-frames — unlike a click in the canvas, which must hold
    // its position.
    setRevealTick((t) => t + 1);
  };

  // A jump-to-resource request from outside the app (the k9s plugin). Held in
  // a ref so the subscription is created once; writing it during render is
  // what the refs lint rule forbids.
  const [notice, setNotice] = useState<string | null>(null);
  // What a handoff is currently discovering, if anything. Also the guard: a
  // second keypress must not stack another pass on top of a running one.
  const [hopping, setHopping] = useState<{ scope: Scope; from: string } | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  // The scope that produced the map a cross-cluster hop replaced, so the user
  // can get back. Only the scope — a snapshot copy would hold its manifests,
  // which are the large part, to serve an undo used once in a while.
  const [replaced, setReplaced] = useState<{ scope: Scope; context: string } | null>(null);

  const land = (id: string) => {
    setSelectedId(id);
    // Start *at* the resource with its children open; the ancestors are one
    // "show parent" click away rather than drawn unasked.
    setTree((cur) => ({ ...cur, rootId: id, expanded: new Set([...cur.expanded, id]) }));
    const url = new URL(window.location.href);
    url.searchParams.set("focus", id);
    window.history.replaceState(null, "", url);
  };

  const onFocusRef = useRef<(r: FocusRequest) => void>(() => {});
  useEffect(() => {
    onFocusRef.current = (req) => {
      if (req.id) {
        land(req.id);
        setNotice(null);
        return;
      }
      const qualified = req.namespace ? `${req.namespace}/${req.name}` : req.name;
      const what = req.kind ? `${req.kind} ${qualified}` : qualified;

      // Nothing to discover: say why, rather than spending minutes of cluster
      // reads to arrive at the same answer.
      if (!req.scope) {
        setNotice(
          req.reason === "unmapped"
            ? `kscope does not map ${req.kind ?? "that kind"} — there is nothing to jump to.`
            : `${what}: no namespace to scope a pass to. Map this cluster first, or press Ctrl-T from a namespaced view.`,
        );
        return;
      }
      // One pass at a time. A held key would otherwise queue a cluster
      // enumeration per repeat.
      if (abortRef.current) return;

      const scope = req.scope;
      const from = snapshot?.cluster?.context ?? "";
      // What the pass costs. A different cluster replaces the map outright; a
      // same-cluster hop keeps the namespaces but decides infra and custom
      // resources from the kind alone, so it can still switch off a layer that
      // was on. Either way the previous scope is worth keeping so it can be
      // restored in one click.
      const crossesCluster = !!(scope.context && from && scope.context !== from);
      const dropsLayer =
        (!!snapshot?.scope.includeInfra && !scope.includeInfra) ||
        (!!snapshot?.scope.includeCRDs && !scope.includeCRDs);
      const losing =
        snapshot && from && (crossesCluster || dropsLayer)
          ? { scope: snapshot.scope, context: from }
          : null;
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setNotice(null);
      setHopping({ scope, from });

      void api
        .refresh(scope, ctrl.signal)
        .then(async (snap) => {
          qc.setQueryData(["graph", "latest"], snap);
          // Resolve against the snapshot we just stored. Server-side, so the
          // tie-breaking rules are the same ones the handoff used.
          const id = await api.resolveFocus({
            name: req.name ?? "",
            namespace: req.namespace,
            kind: req.kind,
          });
          setReplaced(losing);
          if (id) land(id);
          else setNotice(`Discovered ${scope.namespaces.join(", ")}, but ${what} is not in it.`);
        })
        .catch((e: Error) => {
          if (ctrl.signal.aborted) return;
          setNotice(`Couldn't discover ${scope.context || "that cluster"}: ${e.message}`);
        })
        .finally(() => {
          abortRef.current = null;
          setHopping(null);
        });
    };
  });
  useEffect(
    () =>
      onDesktopData<FocusRequest>(DESKTOP_EVENTS.focus, (r) =>
        onFocusRef.current(r),
      ),
    [],
  );

  return (
    <div className="flex h-full flex-col">
      <Header snapshot={snapshot} />
      <div className="flex min-h-0 flex-1">
        {leftOpen && (
          <aside className="flex h-full w-72 flex-col border-r border-slate-200 bg-white">
            <ScopePanel
              snapshot={snapshot}
              onCollapse={() => setLeftOpen(false)}
            />
            <TreePanel
              nodes={nodes}
              selectedId={selected?.id ?? null}
              onSelect={selectAndReveal}
            />
          </aside>
        )}
        <main className="relative flex-1 bg-slate-100">
          {/* Reopen affordance while the sidebar is collapsed — sits where
              the sidebar's own collapse button was, so the control doesn't
              jump around. */}
          {!leftOpen && (
            <button
              type="button"
              onClick={() => setLeftOpen(true)}
              aria-label="Expand sidebar"
              title="Expand sidebar"
              className="absolute left-2 top-2 z-20 flex h-7 w-7 items-center justify-center rounded-md border border-slate-300 bg-white text-slate-500 shadow-sm hover:bg-slate-50 hover:text-slate-700"
            >
              <svg viewBox="0 0 8 8" className="h-2.5 w-2.5 fill-current">
                <path d="M2 0 L6 4 L2 8 Z" />
              </svg>
            </button>
          )}
          {/* Reopen affordance for a collapsed details panel. The panel is
              persistent (cluster overview when nothing is selected), so this
              exists whenever it's collapsed. */}
          {!rightOpen && (
            <button
              type="button"
              onClick={() => setRightOpen(true)}
              aria-label="Expand details"
              title="Expand details"
              className="absolute right-2 top-2 z-20 flex h-7 w-7 items-center justify-center rounded-md border border-slate-300 bg-white text-slate-500 shadow-sm hover:bg-slate-50 hover:text-slate-700"
            >
              <svg viewBox="0 0 8 8" className="h-2.5 w-2.5 fill-current">
                <path d="M6 0 L2 4 L6 8 Z" />
              </svg>
            </button>
          )}
          {!hopping && replaced && (
        <div className="absolute inset-x-0 top-0 z-20 flex items-center gap-3 bg-slate-100 px-4 py-2 text-xs text-slate-700">
          <span className="flex-1">
            Showing <strong>{snapshot?.cluster?.context}</strong>.
          </span>
          <button
            type="button"
            onClick={() => {
              const target = replaced;
              setReplaced(null);
              setHopping({ scope: target.scope, from: snapshot?.cluster?.context ?? "" });
              const ctrl = new AbortController();
              abortRef.current = ctrl;
              void api
                .refresh(target.scope, ctrl.signal)
                .then((snap) => qc.setQueryData(["graph", "latest"], snap))
                .catch((e: Error) => {
                  if (!ctrl.signal.aborted) setNotice(`Couldn't go back: ${e.message}`);
                })
                .finally(() => {
                  abortRef.current = null;
                  setHopping(null);
                });
            }}
            className="shrink-0 rounded border border-slate-300 bg-white px-2 py-0.5 font-medium hover:bg-slate-50"
          >
            ← Back to {replaced.context}
            {replaced.scope.namespaces.length > 0 &&
              ` (${replaced.scope.namespaces.length} namespace${replaced.scope.namespaces.length === 1 ? "" : "s"})`}
          </button>
        </div>
      )}
      {hopping && (
        <div
          role="status"
          className="absolute inset-x-0 top-0 z-30 flex items-center gap-3 bg-sky-100 px-4 py-2 text-xs text-sky-900"
        >
          <span
            aria-hidden
            className="h-3 w-3 shrink-0 animate-spin rounded-full border-2 border-sky-300 border-t-sky-700"
          />
          <span className="flex-1">
            Discovering <strong>{hopping.scope.context || "current context"}</strong>
            {hopping.scope.namespaces.length > 0 && <> · {hopping.scope.namespaces.join(", ")}</>}
            {hopping.from && hopping.from !== hopping.scope.context && (
              <> — this replaces the current map of <strong>{hopping.from}</strong>.</>
            )}
          </span>
          <button
            type="button"
            onClick={() => {
              abortRef.current?.abort();
              abortRef.current = null;
              setHopping(null);
            }}
            className="shrink-0 font-medium underline"
          >
            Cancel
          </button>
        </div>
      )}
      {notice && (
            <div
          role="status"
          className="absolute inset-x-0 top-0 z-20 flex items-start gap-3 bg-amber-100 px-4 py-2 text-xs text-amber-900"
        >
              <span className="flex-1">{notice}</span>
              <button
                type="button"
                onClick={() => setNotice(null)}
                className="shrink-0 font-medium underline"
              >
                Dismiss
              </button>
            </div>
          )}
          {snapshot && !snapshot.cluster && (
            <div className="absolute inset-x-0 top-0 z-10 bg-amber-50 px-4 py-2 text-xs text-amber-800">
              This snapshot predates the current schema — run discovery to
              rebuild it.
            </div>
          )}
          {isLoading && (
            <Centered>
              <span className="text-sm text-slate-500">Loading…</span>
            </Centered>
          )}
          {error && (
            <Centered>
              <span className="text-sm text-red-600">
                Failed to load: {(error as Error).message}
              </span>
            </Centered>
          )}
          {!isLoading && !error && !snapshot && (
            <Centered>
              <div className="max-w-sm text-center text-sm text-slate-500">
                No snapshot yet. Pick one or more namespaces on the left and
                click <b>Run discovery</b>.
              </div>
            </Centered>
          )}
          {snapshot && (
            <GraphCanvas
              visible={visible}
              relations={relations}
              outlines={outlines}
              expanded={tree.expanded}
              childCounts={childCounts}
              marks={marks}
              canShowParent={!!rootParent}
              parentLabel={rootParent?.name}
              selectedId={selected?.id ?? null}
              solo={solo}
              onToggleSolo={() => setSolo((v) => !v)}
              onToggleExpand={toggleNode}
              onToggleGroup={toggleGroupCard}
              onShowParent={climb}
              onGoToRelated={goToRelated}
              spotlight={spotlight}
              revealTick={revealTick}
              onSelect={select}
            />
          )}
        </main>
        {rightOpen && (
          <DetailsPanel
            node={selected}
            byId={byId}
            edges={allEdges}
            snapshot={snapshot}
            snapshotTs={snapshot?.timestamp}
            onSelect={select}
            onCollapse={() => setRightOpen(false)}
          />
        )}
      </div>
    </div>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full w-full items-center justify-center">
      {children}
    </div>
  );
}
