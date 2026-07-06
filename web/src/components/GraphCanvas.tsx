import { useMemo } from "react";
import {
  Background,
  Controls,
  MiniMap,
  ReactFlow,
  type Edge as FlowEdge,
  type Node as FlowNode,
} from "@xyflow/react";
import dagre from "@dagrejs/dagre";
import type { GraphEdge, GraphNode } from "../types/graph";
import {
  EDGE_STYLE,
  HEALTH_HEX,
  INFRA_KINDS,
  health,
  kindAbbrev,
  kindChipClass,
  kindRank,
} from "../lib/display";

interface Props {
  nodes: GraphNode[];
  edges: GraphEdge[];
  /** nodeId → direct children hidden by the focus budget ("+N" chip). */
  hiddenCounts?: Map<string, number>;
  selectedId: string | null;
  onSelect: (node: GraphNode | null) => void;
}

const NODE_W = 200;
const NODE_H = 56;

// Leaf children beyond this count wrap into a grid instead of one endless
// dagre rank — the fix for "the view is too wide".
const WRAP_AT = 6;
const GRID_GAP_X = 16;
const GRID_GAP_Y = 24;
const GRID_PAD = 16;

interface Layout {
  flowNodes: FlowNode[];
  flowEdges: FlowEdge[];
}

// Layered "iceberg" layout:
//   - infra (control-plane, machines) ranks ABOVE the cluster node — dagre
//     ranks by edge direction, so infra containment edges are flipped for
//     layout (and rendered child→parent so the line hangs naturally);
//   - content (namespaces, crds, storage, ...) ranks below;
//   - a parent's leaf children beyond WRAP_AT are laid out as a wrapped grid;
//     an invisible placeholder node reserves the grid's rectangle in dagre so
//     siblings don't collide.
function layout(
  nodes: GraphNode[],
  edges: GraphEdge[],
  hiddenCounts: Map<string, number> | undefined,
  selectedId: string | null,
): Layout {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", nodesep: 30, ranksep: 50 });
  g.setDefaultEdgeLabel(() => ({}));

  const contains = edges.filter((e) => e.kind === "contains");
  const overlay = edges.filter((e) => e.kind !== "contains");

  // Semantic containment (source is always the parent, as App builds them).
  const childIds = new Map<string, string[]>();
  const hasChildren = new Set<string>();
  for (const e of contains) {
    hasChildren.add(e.source);
    const list = childIds.get(e.source);
    if (list) list.push(e.target);
    else childIds.set(e.source, [e.target]);
  }

  // Plan wrapped grids.
  const gridLeaves = new Map<string, string[]>(); // placeholder id → ordered leaf ids
  const inGrid = new Set<string>();
  for (const [parent, kids] of childIds) {
    const leaves = kids.filter((k) => {
      const n = byId.get(k);
      return n && !hasChildren.has(k) && !INFRA_KINDS.has(n.kind);
    });
    if (leaves.length <= WRAP_AT) continue;
    leaves.sort((a, b) => {
      const na = byId.get(a)!;
      const nb = byId.get(b)!;
      return kindRank(na.kind) - kindRank(nb.kind) || na.name.localeCompare(nb.name);
    });
    const rows = Math.ceil(leaves.length / WRAP_AT);
    const phId = `__grid__${parent}`;
    g.setNode(phId, {
      width: WRAP_AT * NODE_W + (WRAP_AT - 1) * GRID_GAP_X + 2 * GRID_PAD,
      height: rows * NODE_H + (rows - 1) * GRID_GAP_Y + 2 * GRID_PAD,
    });
    g.setEdge(parent, phId);
    gridLeaves.set(phId, leaves);
    for (const l of leaves) inGrid.add(l);
  }

  for (const n of nodes) {
    if (!inGrid.has(n.id)) g.setNode(n.id, { width: NODE_W, height: NODE_H });
  }

  // Containment drives the layout; infra edges are flipped so those subtrees
  // grow upward from the cluster.
  for (const e of contains) {
    if (inGrid.has(e.target)) continue; // grid members are placed manually
    const child = byId.get(e.target);
    if (child && INFRA_KINDS.has(child.kind)) g.setEdge(e.target, e.source);
    else g.setEdge(e.source, e.target);
  }

  // Relationship edges don't warp ranks — except to anchor nodes pulled in
  // purely via a relationship (they have no containment edge in view).
  const anchored = new Set<string>();
  for (const e of contains) {
    anchored.add(e.source);
    anchored.add(e.target);
  }
  for (const e of overlay) {
    if (inGrid.has(e.source) || inGrid.has(e.target)) continue;
    if (!anchored.has(e.source) || !anchored.has(e.target)) {
      g.setEdge(e.source, e.target);
    }
  }

  dagre.layout(g);

  // Positions: dagre for regular nodes, computed grid slots for wrapped ones
  // (each partial row centered within the placeholder rectangle).
  const pos = new Map<string, { x: number; y: number }>();
  for (const n of nodes) {
    if (inGrid.has(n.id)) continue;
    const p = g.node(n.id);
    if (p) pos.set(n.id, { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 });
  }
  // Grid members live INSIDE a rendered group container (one containment
  // edge into the box instead of one per leaf); positions are relative.
  const gridParentOf = new Map<string, string>();
  const groupNodes: FlowNode[] = [];
  for (const [phId, leaves] of gridLeaves) {
    const ph = g.node(phId);
    if (!ph) continue;
    groupNodes.push({
      id: phId,
      position: { x: ph.x - ph.width / 2, y: ph.y - ph.height / 2 },
      data: { label: null },
      selectable: false,
      style: {
        width: ph.width,
        height: ph.height,
        background: "rgba(148,163,184,0.07)",
        border: "1px dashed #e2e8f0",
        borderRadius: 12,
      },
    });
    leaves.forEach((id, i) => {
      const row = Math.floor(i / WRAP_AT);
      const col = i % WRAP_AT;
      const inRow = Math.min(WRAP_AT, leaves.length - row * WRAP_AT);
      const rowLeft =
        GRID_PAD +
        (ph.width - 2 * GRID_PAD - (inRow * NODE_W + (inRow - 1) * GRID_GAP_X)) / 2;
      gridParentOf.set(id, phId);
      pos.set(id, {
        x: rowLeft + col * (NODE_W + GRID_GAP_X),
        y: GRID_PAD + row * (NODE_H + GRID_GAP_Y),
      });
    });
  }

  const flowNodes: FlowNode[] = [...groupNodes];
  for (const n of nodes) {
    const p = pos.get(n.id);
    if (!p) continue;
    const gridParent = gridParentOf.get(n.id);
    const hex = HEALTH_HEX[health(n)];
    const isSelected = n.id === selectedId;
    const hiddenKids = hiddenCounts?.get(n.id) ?? 0;
    flowNodes.push({
      id: n.id,
      position: p,
      ...(gridParent ? { parentId: gridParent, extent: "parent" as const } : {}),
      data: {
        label: (
          <div className="flex w-full items-center gap-2 text-left">
            <span
              className={`shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold ${kindChipClass(n.kind)}`}
            >
              {kindAbbrev(n.kind)}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-xs font-medium text-slate-900">
                {n.name}
              </span>
              <span className="block text-[10px] text-slate-400">{n.kind}</span>
            </span>
            {n.gitops && (
              <span
                title={`Managed by Flux ${n.gitops.kind} ${n.gitops.namespace}/${n.gitops.name}`}
                className="shrink-0 rounded bg-fuchsia-50 px-1 text-[9px] font-semibold text-fuchsia-600"
              >
                flux
              </span>
            )}
            {hiddenKids > 0 && (
              <span
                title={`${hiddenKids} more inside — click to focus`}
                className="shrink-0 rounded-full bg-slate-200 px-1.5 text-[10px] font-medium text-slate-600"
              >
                +{hiddenKids}
              </span>
            )}
          </div>
        ),
        raw: n,
      },
      style: {
        width: NODE_W,
        padding: 8,
        borderRadius: 8,
        // Health shows as a left accent stripe; selection as a blue ring.
        // Synthetic (logical) nodes are dashed — there's no API object there.
        border: isSelected
          ? "2px solid #3b82f6"
          : `1px ${n.synthetic ? "dashed" : "solid"} #cbd5e1`,
        boxShadow: isSelected
          ? "0 0 0 3px rgba(59,130,246,0.25)"
          : `inset 3px 0 0 ${hex}`,
        background: "#fff",
        fontSize: 12,
      },
    });
  }

  const flowEdges: FlowEdge[] = [];
  for (const [phId] of gridLeaves) {
    // One containment edge into the box replaces one edge per grid member.
    const parent = phId.slice("__grid__".length);
    flowEdges.push({
      id: `${parent}->${phId}`,
      source: parent,
      target: phId,
      style: { stroke: "#cbd5e1" },
    });
  }
  for (const e of edges) {
    const rel = EDGE_STYLE[e.kind];
    if (e.kind === "contains" && inGrid.has(e.target)) continue; // → group edge
    // Wiring that touches grid members stays visible (an orphaned ConfigMap
    // should LOOK different from a wired one) but is dimmed and unlabeled
    // unless it touches the selection — signal without the spaghetti.
    const dimmed =
      !!rel &&
      (inGrid.has(e.source) || inGrid.has(e.target)) &&
      e.source !== selectedId &&
      e.target !== selectedId;
    const child = byId.get(e.target);
    // Infra containment renders child→parent so the line hangs from the
    // upper (infra) node down into the cluster instead of looping around.
    const flip = e.kind === "contains" && child && INFRA_KINDS.has(child.kind);
    flowEdges.push({
      id: e.id,
      source: flip ? e.target : e.source,
      target: flip ? e.source : e.target,
      label: e.kind === "contains" || dimmed ? undefined : e.kind,
      labelStyle: { fontSize: 10, fill: rel?.stroke ?? "#64748b" },
      // Orthogonal routing for relationship edges — bezier curves between
      // same-rank siblings (e.g. scheduler → api-server) loop unpleasantly.
      type: rel ? "smoothstep" : undefined,
      style: rel
        ? { stroke: rel.stroke, strokeDasharray: "6 3", opacity: dimmed ? 0.3 : 1 }
        : { stroke: "#cbd5e1" },
    });
  }

  return { flowNodes, flowEdges };
}

export function GraphCanvas({
  nodes,
  edges,
  hiddenCounts,
  selectedId,
  onSelect,
}: Props) {
  const { flowNodes, flowEdges } = useMemo(
    () => layout(nodes, edges, hiddenCounts, selectedId),
    [nodes, edges, hiddenCounts, selectedId],
  );

  return (
    <div className="h-full w-full">
      <ReactFlow
        // Remount when the focus root changes so fitView re-frames the new
        // subgraph — simpler than driving the viewport imperatively.
        key={selectedId ?? "root"}
        nodes={flowNodes}
        edges={flowEdges}
        fitView
        // Whole-cluster views are wide; the default minZoom (0.5) would stop
        // fitView from actually fitting them.
        minZoom={0.04}
        maxZoom={1.25}
        onNodeClick={(_, n) => {
          const raw = (n.data as { raw?: GraphNode }).raw;
          if (raw) onSelect(raw);
        }}
        onPaneClick={() => onSelect(null)}
      >
        <Background />
        <Controls />
        {flowNodes.length > 15 && <MiniMap pannable zoomable />}
      </ReactFlow>
    </div>
  );
}
