import { describe, expect, it } from "vitest";
import dagre from "@dagrejs/dagre";
import type { GraphNode } from "../types/graph";
import { groupId, relate, visibleTree } from "./tree";

// Spec §7: expanding a large group lays out hundreds of nodes through dagre
// synchronously, and the breaking point was unmeasured. This pins it. The
// numbers are deliberately loose — this guards against an order-of-magnitude
// regression (an accidental quadratic), not against CI jitter.
function bigSnapshot(pods: number): GraphNode[] {
  const nodes: GraphNode[] = [
    { id: "cluster", kind: "Cluster", name: "c", health: "healthy" },
    { id: "ns", kind: "Namespace", name: "ns", parentId: "cluster", health: "healthy" },
    { id: "d", kind: "Deployment", name: "d", parentId: "ns", health: "healthy" },
  ];
  for (let i = 0; i < pods; i++) {
    nodes.push({
      id: `p/${i}`,
      kind: "Pod",
      name: `pod-${i}`,
      parentId: "d",
      health: "healthy",
    });
  }
  return nodes;
}

const layoutCost = (nodeIds: string[], edges: [string, string][]) => {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", nodesep: 44, ranksep: 88 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const id of nodeIds) g.setNode(id, { width: 200, height: 56 });
  for (const [s, t] of edges) g.setEdge(s, t);
  const t0 = performance.now();
  dagre.layout(g);
  return performance.now() - t0;
};

describe("large-group performance", () => {
  const pods = 312;
  const all = bigSnapshot(pods);
  const gid = groupId("d", "Pod");
  const open = {
    rootId: "cluster",
    expanded: new Set(["cluster", "ns", "d"]),
    expandedGroups: new Set([gid]),
  };

  it("keeps the pure layer well under a frame", () => {
    const t0 = performance.now();
    const v = visibleTree(all, open);
    const edges = all
      .filter((n) => n.kind === "Pod")
      .map((n) => ({ id: `e${n.id}`, source: n.id, target: "ns", kind: "uses" }));
    relate(all, edges, v, "p/0");
    const ms = performance.now() - t0;
    expect(v.nodes).toHaveLength(pods + 3);
    expect(ms).toBeLessThan(100);
  });

  it("stays linear-ish as a group grows, rather than quadratic", () => {
    const small = visibleTree(bigSnapshot(50), {
      ...open,
      expandedGroups: new Set([gid]),
    });
    const big = visibleTree(all, open);
    const cost = (v: ReturnType<typeof visibleTree>) =>
      layoutCost(
        [...v.nodes.map((n) => n.id), ...v.groups.map((g) => g.id)],
        v.containment.map((e) => [e.source, e.target] as [string, string]),
      );
    const smallMs = cost(small);
    const bigMs = cost(big);
    // 6x the nodes. Quadratic would be ~36x; allow a generous 20x for noise on
    // a loaded CI runner while still catching a real blow-up.
    expect(bigMs).toBeLessThan(Math.max(smallMs * 20, 50));
  });
});
