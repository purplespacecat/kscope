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
import { EDGE_STYLE, HEALTH_HEX, health, kindAbbrev } from "../lib/display";

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

// Dagre gives us a top-to-bottom layered layout. We rerun it whenever the
// focus subgraph changes — cheap at focused sizes.
function layout(
  nodes: GraphNode[],
  edges: GraphEdge[],
  hiddenCounts: Map<string, number> | undefined,
  selectedId: string | null,
): { flowNodes: FlowNode[]; flowEdges: FlowEdge[] } {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", nodesep: 30, ranksep: 50 });
  g.setDefaultEdgeLabel(() => ({}));

  for (const n of nodes) g.setNode(n.id, { width: NODE_W, height: NODE_H });

  // Only containment edges drive the layout — hierarchy is position,
  // relationships are an overlay that shouldn't warp the ranks. Exception:
  // nodes pulled in purely via a relationship (a pod's ConfigMaps) have no
  // containment edge in view, so their relationship edge anchors them.
  const anchored = new Set<string>();
  for (const e of edges) {
    if (e.kind === "contains") {
      anchored.add(e.source);
      anchored.add(e.target);
    }
  }
  for (const e of edges) {
    if (e.kind === "contains") {
      g.setEdge(e.source, e.target);
    } else if (!anchored.has(e.source) || !anchored.has(e.target)) {
      g.setEdge(e.source, e.target);
    }
  }
  dagre.layout(g);

  const flowNodes: FlowNode[] = nodes.map((n) => {
    const { x, y } = g.node(n.id);
    const hex = HEALTH_HEX[health(n)];
    const isSelected = n.id === selectedId;
    const hiddenKids = hiddenCounts?.get(n.id) ?? 0;
    return {
      id: n.id,
      position: { x: x - NODE_W / 2, y: y - NODE_H / 2 },
      data: {
        label: (
          <div className="flex w-full items-center gap-2 text-left">
            <span className="shrink-0 rounded bg-slate-100 px-1 py-0.5 text-[10px] font-semibold text-slate-500">
              {kindAbbrev(n.kind)}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-xs font-medium text-slate-900">
                {n.name}
              </span>
              <span className="block text-[10px] text-slate-400">{n.kind}</span>
            </span>
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
        border: isSelected ? "2px solid #3b82f6" : "1px solid #cbd5e1",
        boxShadow: isSelected
          ? "0 0 0 3px rgba(59,130,246,0.25)"
          : `inset 3px 0 0 ${hex}`,
        background: "#fff",
        fontSize: 12,
      },
    };
  });

  const flowEdges: FlowEdge[] = edges.map((e) => {
    // Containment is implied by the layout; labeling every edge "contains"
    // would be noise. Relationship kinds are labeled, colored and dashed.
    const rel = EDGE_STYLE[e.kind];
    return {
      id: e.id,
      source: e.source,
      target: e.target,
      label: e.kind === "contains" ? undefined : e.kind,
      labelStyle: { fontSize: 10, fill: rel?.stroke ?? "#64748b" },
      style: rel
        ? { stroke: rel.stroke, strokeDasharray: "6 3" }
        : { stroke: "#cbd5e1" },
    };
  });

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
        onNodeClick={(_, n) => onSelect((n.data as { raw: GraphNode }).raw)}
        onPaneClick={() => onSelect(null)}
      >
        <Background />
        <Controls />
        {flowNodes.length > 15 && <MiniMap pannable zoomable />}
      </ReactFlow>
    </div>
  );
}
