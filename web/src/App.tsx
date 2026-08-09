import { useEffect, useMemo, useRef, useState } from "react";
import { Header } from "./components/Header";
import { ScopePanel } from "./components/ScopePanel";
import { TreePanel } from "./components/TreePanel";
import { GraphCanvas } from "./components/GraphCanvas";
import { DetailsPanel } from "./components/DetailsPanel";
import { useLatest } from "./hooks/useGraph";
import {
  DESKTOP_EVENTS,
  onDesktopData,
  type FocusRequest,
} from "./lib/desktop";
import type { GraphEdge, GraphNode } from "./types/graph";

// Stable empty arrays so hooks downstream don't re-fire while loading.
const NO_NODES: GraphNode[] = [];
const NO_EDGES: GraphEdge[] = [];

// Cap on nodes in one focus view. Layers past the budget are hidden behind a
// "+N" chip — one click on the parent reveals them. Keeps dagre readable:
// the cluster default shows just the namespace fan, a namespace shows its
// workloads (and pods when they fit).
const NODE_BUDGET = 40;

// Focus = the selected node's ancestry spine (context: where it lives) plus
// its descendant layers (content: what it contains), drawn as "contains"
// edges. No selection focuses the cluster root — the whole map.
function focusSubgraph(
  all: GraphNode[],
  allEdges: GraphEdge[],
  selectedId: string | null,
): { nodes: GraphNode[]; edges: GraphEdge[]; hidden: Map<string, number> } {
  const hidden = new Map<string, number>();
  if (all.length === 0) return { nodes: [], edges: [], hidden };
  const byId = new Map(all.map((n) => [n.id, n]));
  const children = new Map<string, GraphNode[]>();
  for (const n of all) {
    if (!n.parentId || !byId.has(n.parentId)) continue;
    const list = children.get(n.parentId);
    if (list) list.push(n);
    else children.set(n.parentId, [n]);
  }

  const root =
    (selectedId ? byId.get(selectedId) : undefined) ??
    all.find((n) => !n.parentId) ??
    all[0];

  const nodes: GraphNode[] = [];
  const edges: GraphEdge[] = [];
  const seen = new Set<string>();
  const add = (n: GraphNode) => {
    if (!seen.has(n.id)) {
      seen.add(n.id);
      nodes.push(n);
    }
  };

  // Ancestry spine, root-of-tree first.
  const spine: GraphNode[] = [];
  for (
    let cur: GraphNode | undefined = root;
    cur;
    cur = cur.parentId ? byId.get(cur.parentId) : undefined
  ) {
    spine.push(cur);
  }
  spine.reverse();
  for (const n of spine) add(n);
  for (let i = 0; i + 1 < spine.length; i++) {
    edges.push({
      id: `${spine[i].id}->${spine[i + 1].id}`,
      source: spine[i].id,
      target: spine[i + 1].id,
      kind: "contains",
    });
  }

  // Descend level by level under the focus root, stopping before a level
  // that would blow the budget — deeper layers are one click away. The first
  // level below the root is always included so a selection never looks empty.
  let frontier = [root];
  let level = 0;
  while (frontier.length > 0) {
    const next = frontier.flatMap((p) => children.get(p.id) ?? []);
    if (next.length === 0) break;
    if (level > 0 && nodes.length + next.length > NODE_BUDGET) {
      for (const parent of frontier) {
        const kids = children.get(parent.id) ?? [];
        if (kids.length > 0) hidden.set(parent.id, kids.length);
      }
      break;
    }
    for (const parent of frontier) {
      for (const child of children.get(parent.id) ?? []) {
        add(child);
        edges.push({
          id: `${parent.id}->${child.id}`,
          source: parent.id,
          target: child.id,
          kind: "contains",
        });
      }
    }
    frontier = next;
    level++;
  }

  // Relationship overlay: wiring between nodes already in view (a namespace
  // focus shows its services selecting its pods), plus the selected node's
  // own 1-hop neighbors — pulled in even from outside the containment view
  // (a pod focus shows the ConfigMaps it mounts).
  for (const e of allEdges) {
    const srcIn = seen.has(e.source);
    const tgtIn = seen.has(e.target);
    const touchesSelected =
      selectedId !== null &&
      (e.source === selectedId || e.target === selectedId);
    if (!(srcIn && tgtIn) && !touchesSelected) continue;
    const src = byId.get(e.source);
    const tgt = byId.get(e.target);
    if (!src || !tgt) continue;
    add(src);
    add(tgt);
    edges.push(e);
  }

  return { nodes, edges, hidden };
}

export default function App() {
  const { data: snapshot, isLoading, error } = useLatest();
  // Selection is stored as an id, seeded from ?focus= so any view is a
  // shareable URL; the node object is derived from the current snapshot, so
  // selection survives snapshot refreshes when the resource still exists.
  const [selectedId, setSelectedId] = useState<string | null>(
    () => new URLSearchParams(window.location.search).get("focus"),
  );

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

  const select = (n: GraphNode | null) => {
    setSelectedId(n?.id ?? null);
    const url = new URL(window.location.href);
    if (n) url.searchParams.set("focus", n.id);
    else url.searchParams.delete("focus");
    window.history.replaceState(null, "", url);
  };

  const focus = useMemo(
    () => focusSubgraph(nodes, allEdges, selected?.id ?? null),
    [nodes, allEdges, selected],
  );

  // A jump-to-resource request from outside the app (the k9s plugin). Held in
  // a ref so the subscription is created once; writing it during render is
  // what the refs lint rule forbids.
  const [notice, setNotice] = useState<string | null>(null);
  const onFocusRef = useRef<(r: FocusRequest) => void>(() => {});
  useEffect(() => {
    onFocusRef.current = (req) => {
      if (req.id) {
        setSelectedId(req.id);
        const url = new URL(window.location.href);
        url.searchParams.set("focus", req.id);
        window.history.replaceState(null, "", url);
        setNotice(null);
        return;
      }
      // Not in this snapshot. Saying so beats looking like the keystroke was
      // swallowed; the user's fix is to widen the scope and re-run discovery.
      const qualified = req.namespace ? `${req.namespace}/${req.name}` : req.name;
      const what = req.kind ? `${req.kind} ${qualified}` : qualified;
      setNotice(
        req.context
          ? `${what} is in context "${req.context}", but this snapshot is from "${snapshot?.cluster?.context ?? "another cluster"}".`
          : `${what} isn't in this snapshot — run discovery including its namespace.`,
      );
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
              onSelect={select}
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
          {notice && (
            <div className="absolute inset-x-0 top-0 z-20 flex items-start gap-3 bg-amber-100 px-4 py-2 text-xs text-amber-900">
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
              nodes={focus.nodes}
              edges={focus.edges}
              hiddenCounts={focus.hidden}
              selectedId={selected?.id ?? null}
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
