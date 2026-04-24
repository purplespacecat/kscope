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
import type { GraphEdge, GraphNode, Snapshot } from "../types/graph";

interface Props {
  snapshot: Snapshot;
  onSelect: (node: GraphNode | null) => void;
}

// Dagre gives us a top-to-bottom layered layout. We rerun it whenever the
// snapshot changes — cheap at current sizes; revisit when graphs get large.
function layout(
  nodes: GraphNode[],
  edges: GraphEdge[],
): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", nodesep: 40, ranksep: 60 });
  g.setDefaultEdgeLabel(() => ({}));

  const NODE_W = 180;
  const NODE_H = 56;

  for (const n of nodes) g.setNode(n.id, { width: NODE_W, height: NODE_H });
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);

  const laidOutNodes: FlowNode[] = nodes.map((n) => {
    const { x, y } = g.node(n.id);
    return {
      id: n.id,
      position: { x: x - NODE_W / 2, y: y - NODE_H / 2 },
      data: { label: `${n.kind}: ${n.name}`, raw: n },
      style: {
        width: NODE_W,
        padding: 8,
        borderRadius: 8,
        border: "1px solid #cbd5e1",
        background: "#fff",
        fontSize: 12,
      },
    };
  });

  const laidOutEdges: FlowEdge[] = edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    label: e.kind,
    labelStyle: { fontSize: 10, fill: "#64748b" },
  }));

  return { nodes: laidOutNodes, edges: laidOutEdges };
}

export function GraphCanvas({ snapshot, onSelect }: Props) {
  const { nodes, edges } = useMemo(
    () => layout(snapshot.nodes, snapshot.edges),
    [snapshot],
  );

  return (
    <div className="h-full w-full">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        onNodeClick={(_, n) => onSelect((n.data as { raw: GraphNode }).raw)}
        onPaneClick={() => onSelect(null)}
      >
        <Background />
        <Controls />
        <MiniMap pannable zoomable />
      </ReactFlow>
    </div>
  );
}
