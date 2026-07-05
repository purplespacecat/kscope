import { useEffect, useMemo, useState } from "react";
import type { GraphNode, Health } from "../types/graph";
import {
  HEALTH_DOT,
  health,
  kindAbbrev,
  kindRank,
  worseOf,
} from "../lib/display";

interface Props {
  nodes: GraphNode[];
  selectedId: string | null;
  onSelect: (node: GraphNode) => void;
}

interface TreeIndex {
  byId: Map<string, GraphNode>;
  children: Map<string, GraphNode[]>;
  roots: GraphNode[];
  /** Worst health in each node's subtree (itself included). */
  worst: Map<string, Health>;
}

// The containment hierarchy is fully described by parentId, so the tree is a
// pure client-side view over the snapshot — expanding costs no fetches.
function buildIndex(nodes: GraphNode[]): TreeIndex {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const children = new Map<string, GraphNode[]>();
  const roots: GraphNode[] = [];
  for (const n of nodes) {
    if (n.parentId && byId.has(n.parentId)) {
      const list = children.get(n.parentId);
      if (list) list.push(n);
      else children.set(n.parentId, [n]);
    } else {
      roots.push(n);
    }
  }
  const byKindThenName = (a: GraphNode, b: GraphNode) =>
    kindRank(a.kind) - kindRank(b.kind) || a.name.localeCompare(b.name);
  for (const list of children.values()) list.sort(byKindThenName);
  roots.sort(byKindThenName);

  // Roll the worst descendant health up to every ancestor: a namespace with a
  // crashing pod shows red while collapsed, so problems surface at the root.
  const worst = new Map<string, Health>();
  const rollup = (n: GraphNode): Health => {
    let w = health(n);
    for (const c of children.get(n.id) ?? []) w = worseOf(w, rollup(c));
    worst.set(n.id, w);
    return w;
  };
  for (const r of roots) rollup(r);

  return { byId, children, roots, worst };
}

export function TreePanel({ nodes, selectedId, onSelect }: Props) {
  const { byId, children, roots, worst } = useMemo(
    () => buildIndex(nodes),
    [nodes],
  );
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");

  // A new snapshot is a new world: reset to just the roots (cluster) open so
  // the namespace list shows immediately.
  useEffect(() => {
    setExpanded(new Set(roots.map((r) => r.id)));
  }, [roots]);

  // Selecting from the graph should reveal the node in the tree too.
  useEffect(() => {
    if (!selectedId) return;
    setExpanded((prev) => {
      const next = new Set(prev);
      let cur = byId.get(selectedId);
      while (cur?.parentId) {
        next.add(cur.parentId);
        cur = byId.get(cur.parentId);
      }
      return next;
    });
  }, [selectedId, byId]);

  // A filter match keeps its whole ancestry visible so the path stays legible.
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return null;
    const keep = new Set<string>();
    for (const n of nodes) {
      if (!n.name.toLowerCase().includes(q) && !n.kind.toLowerCase().includes(q)) {
        continue;
      }
      for (
        let cur: GraphNode | undefined = n;
        cur && !keep.has(cur.id);
        cur = cur.parentId ? byId.get(cur.parentId) : undefined
      ) {
        keep.add(cur.id);
      }
    }
    return keep;
  }, [query, nodes, byId]);

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  function Row({ node, depth }: { node: GraphNode; depth: number }) {
    const all = children.get(node.id) ?? [];
    const kids = visible ? all.filter((k) => visible.has(k.id)) : all;
    const isOpen = visible ? true : expanded.has(node.id); // filtering auto-expands
    const isSelected = node.id === selectedId;
    return (
      <>
        <div
          className={`flex w-full cursor-pointer items-center gap-1.5 py-1 pr-2 text-sm ${
            isSelected
              ? "bg-blue-50 text-blue-900"
              : "text-slate-700 hover:bg-slate-50"
          }`}
          style={{ paddingLeft: depth * 14 + 8 }}
          onClick={() => onSelect(node)}
        >
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              toggle(node.id);
            }}
            className={`flex h-4 w-4 shrink-0 items-center justify-center rounded text-slate-400 hover:bg-slate-200 hover:text-slate-700 ${
              all.length === 0 ? "invisible" : ""
            }`}
            aria-label={isOpen ? "Collapse" : "Expand"}
          >
            <svg
              viewBox="0 0 8 8"
              className={`h-2 w-2 fill-current transition-transform ${isOpen ? "rotate-90" : ""}`}
            >
              <path d="M1 0 L7 4 L1 8 Z" />
            </svg>
          </button>
          <span className="w-9 shrink-0 rounded bg-slate-100 px-1 text-center text-[10px] font-semibold text-slate-500">
            {kindAbbrev(node.kind)}
          </span>
          <span
            className="min-w-0 flex-1 truncate"
            title={`${node.kind}: ${node.name}`}
          >
            {node.name}
          </span>
          {!isOpen && all.length > 0 && (
            <span className="text-[10px] text-slate-400">{all.length}</span>
          )}
          <span
            className={`h-2 w-2 shrink-0 rounded-full ${HEALTH_DOT[worst.get(node.id) ?? health(node)]}`}
          />
        </div>
        {isOpen &&
          kids.map((k) => <Row key={k.id} node={k} depth={depth + 1} />)}
      </>
    );
  }

  const visibleRoots = visible ? roots.filter((r) => visible.has(r.id)) : roots;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-slate-200 p-3">
        <input
          type="text"
          placeholder="Find resource…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="w-full rounded border border-slate-300 px-2 py-1 text-sm outline-none focus:border-slate-500"
        />
      </div>
      <div className="flex-1 overflow-y-auto py-1">
        {roots.length === 0 && (
          <p className="px-4 py-2 text-xs text-slate-400">Snapshot is empty.</p>
        )}
        {visibleRoots.length === 0 && roots.length > 0 && (
          <p className="px-4 py-2 text-xs text-slate-400">No matches.</p>
        )}
        {visibleRoots.map((r) => (
          <Row key={r.id} node={r} depth={0} />
        ))}
      </div>
    </div>
  );
}
