import { describe, expect, it } from "vitest";
import type { GraphEdge, GraphNode } from "../types/graph";
import {
  controllerMarks,
  prune,
  resolveEdges,
  reveal,
  showParent,
  toggleExpand,
  type TreeState,
  visibleTree,
} from "./tree";

// cluster
// ├── ns app
// │   ├── D web
// │   │   └── RS web-1
// │   │       └── P web-1-a
// │   ├── CM a, CM b, CM c        (3 leaves of one kind → group card)
// │   └── SVC web
// └── ns other
const n = (id: string, kind: string, name: string, parentId?: string): GraphNode => ({
  id,
  kind,
  name,
  parentId,
  health: "healthy",
});

const NODES: GraphNode[] = [
  n("cluster", "Cluster", "dev/ci1"),
  n("ns/app", "Namespace", "app", "cluster"),
  n("ns/other", "Namespace", "other", "cluster"),
  n("d/web", "Deployment", "web", "ns/app"),
  n("rs/web-1", "ReplicaSet", "web-1", "d/web"),
  n("p/web-1-a", "Pod", "web-1-a", "rs/web-1"),
  n("cm/a", "ConfigMap", "a", "ns/app"),
  n("cm/b", "ConfigMap", "b", "ns/app"),
  n("cm/c", "ConfigMap", "c", "ns/app"),
  n("svc/web", "Service", "web", "ns/app"),
  n("hr/web", "HelmRelease", "web", "ns/app"),
];

const state = (over: Partial<TreeState> = {}): TreeState => ({
  rootId: "cluster",
  expanded: new Set<string>(),
  expandedGroups: new Set<string>(),
  ...over,
});

const ids = (v: { nodes: GraphNode[] }) => v.nodes.map((x) => x.id).sort();

describe("visibleTree", () => {
  it("shows only the root when nothing is expanded", () => {
    const v = visibleTree(NODES, state());
    expect(ids(v)).toEqual(["cluster"]);
    expect(v.groups).toHaveLength(0);
    expect(v.containment).toHaveLength(0);
  });

  it("shows a node's children only when that node is expanded", () => {
    const v = visibleTree(NODES, state({ expanded: new Set(["cluster"]) }));
    expect(ids(v)).toEqual(["cluster", "ns/app", "ns/other"]);
    // One level only: ns/app is visible but not expanded, so D web is not.
    expect(v.containment.map((e) => `${e.source}->${e.target}`).sort()).toEqual([
      "cluster->ns/app",
      "cluster->ns/other",
    ]);
  });

  it("descends only through expanded nodes, however deep", () => {
    const v = visibleTree(
      NODES,
      state({ expanded: new Set(["cluster", "ns/app", "d/web", "rs/web-1"]) }),
    );
    expect(ids(v)).toContain("p/web-1-a");
  });

  it("does not descend through a node that is visible but collapsed", () => {
    const v = visibleTree(NODES, state({ expanded: new Set(["cluster", "ns/app"]) }));
    expect(ids(v)).toContain("d/web");
    expect(ids(v)).not.toContain("rs/web-1");
  });

  it("starts from rootId, not from the tree root", () => {
    const v = visibleTree(NODES, state({ rootId: "d/web", expanded: new Set(["d/web"]) }));
    expect(ids(v)).toEqual(["d/web", "rs/web-1"]);
    expect(ids(v)).not.toContain("cluster");
  });
});

describe("visibleTree grouping", () => {
  const appOpen = state({ expanded: new Set(["cluster", "ns/app"]) });

  it("folds three or more same-kind leaf siblings into one group card", () => {
    const v = visibleTree(NODES, appOpen);
    expect(ids(v)).not.toContain("cm/a");
    expect(v.groups.map((g) => g.kind)).toEqual(["ConfigMap"]);
    expect(v.groups[0].memberIds.sort()).toEqual(["cm/a", "cm/b", "cm/c"]);
    expect(v.groups[0].parentId).toBe("ns/app");
  });

  it("leaves a lone sibling of its kind as an ordinary card", () => {
    const v = visibleTree(NODES, appOpen);
    expect(ids(v)).toContain("svc/web");
  });

  it("never groups a node that has children of its own", () => {
    // Three Deployments would be three cards, not a group, if each has a RS.
    const withDeps: GraphNode[] = [
      ...NODES,
      n("d/two", "Deployment", "two", "ns/app"),
      n("rs/two-1", "ReplicaSet", "two-1", "d/two"),
      n("d/three", "Deployment", "three", "ns/app"),
      n("rs/three-1", "ReplicaSet", "three-1", "d/three"),
    ];
    const v = visibleTree(withDeps, appOpen);
    expect(v.groups.map((g) => g.kind)).not.toContain("Deployment");
    expect(ids(v)).toContain("d/two");
  });

  it("never groups Namespaces — they are the drill path", () => {
    const many: GraphNode[] = [
      ...NODES,
      n("ns/c", "Namespace", "c", "cluster"),
      n("ns/d", "Namespace", "d", "cluster"),
    ];
    const v = visibleTree(many, state({ expanded: new Set(["cluster"]) }));
    expect(v.groups).toHaveLength(0);
    expect(ids(v)).toContain("ns/c");
  });

  it("shows members and their containment once the group is expanded", () => {
    const gid = "__kg__ns/app__ConfigMap";
    const v = visibleTree(
      NODES,
      state({ expanded: new Set(["cluster", "ns/app"]), expandedGroups: new Set([gid]) }),
    );
    expect(ids(v)).toContain("cm/a");
    expect(v.groups[0].expanded).toBe(true);
    const arcs = v.containment.map((e) => `${e.source}->${e.target}`);
    expect(arcs).toContain(`ns/app->${gid}`);
    expect(arcs).toContain(`${gid}->cm/a`);
  });

  it("does not group under a collapsed parent — nothing there is visible", () => {
    const v = visibleTree(NODES, state({ expanded: new Set(["cluster"]) }));
    expect(v.groups).toHaveLength(0);
  });
});

describe("toggleExpand", () => {
  it("expands a collapsed node and collapses an expanded one", () => {
    const a = toggleExpand(NODES, state(), "cluster", false);
    expect(a.expanded.has("cluster")).toBe(true);
    const b = toggleExpand(NODES, a, "cluster", false);
    expect(b.expanded.has("cluster")).toBe(false);
  });

  it("remembers a sub-branch across a collapse and re-expand", () => {
    const open = state({ expanded: new Set(["cluster", "ns/app", "d/web", "rs/web-1"]) });
    const closed = toggleExpand(NODES, open, "ns/app", false);
    const reopened = toggleExpand(NODES, closed, "ns/app", false);
    expect([...reopened.expanded].sort()).toEqual([...open.expanded].sort());
    expect(ids(visibleTree(NODES, reopened))).toContain("p/web-1-a");
  });

  it("leaves the caller's state object untouched", () => {
    const before = state();
    toggleExpand(NODES, before, "cluster", false);
    expect(before.expanded.size).toBe(0);
  });

  describe("with solo mode on", () => {
    it("collapses the expanded node's siblings", () => {
      const open = state({ expanded: new Set(["cluster", "ns/app"]) });
      const next = toggleExpand(NODES, open, "ns/other", true);
      expect(next.expanded.has("ns/other")).toBe(true);
      expect(next.expanded.has("ns/app")).toBe(false);
      expect(next.expanded.has("cluster")).toBe(true); // the parent is not a sibling
    });

    it("does not touch the expanded node's own sub-branch", () => {
      const open = state({ expanded: new Set(["cluster", "d/web", "rs/web-1"]) });
      const next = toggleExpand(NODES, open, "ns/app", true);
      expect(next.expanded.has("d/web")).toBe(true);
      expect(next.expanded.has("rs/web-1")).toBe(true);
    });

    it("does not collapse group cards beside the expanded one", () => {
      const gid = "__kg__ns/app__ConfigMap";
      const open = state({
        expanded: new Set(["cluster", "ns/app"]),
        expandedGroups: new Set([gid]),
      });
      const next = toggleExpand(NODES, open, "d/web", true);
      expect(next.expandedGroups.has(gid)).toBe(true);
    });

    it("collapsing with solo on collapses nothing else", () => {
      const open = state({ expanded: new Set(["cluster", "ns/app", "ns/other"]) });
      const next = toggleExpand(NODES, open, "ns/app", true);
      expect(next.expanded.has("ns/app")).toBe(false);
      expect(next.expanded.has("ns/other")).toBe(true);
    });
  });
});

describe("showParent", () => {
  it("raises the root and keeps the branch below it open", () => {
    const at = state({ rootId: "d/web", expanded: new Set(["d/web"]) });
    const up = showParent(NODES, at);
    expect(up.rootId).toBe("ns/app");
    expect(up.expanded.has("ns/app")).toBe(true);
    expect(ids(visibleTree(NODES, up))).toContain("rs/web-1");
  });

  it("is a no-op at the top of the tree", () => {
    const at = state({ rootId: "cluster" });
    expect(showParent(NODES, at).rootId).toBe("cluster");
  });
});

describe("reveal", () => {
  it("expands exactly the ancestor chain down to the target", () => {
    const next = reveal(NODES, state(), "p/web-1-a", false);
    expect([...next.expanded].sort()).toEqual(
      ["cluster", "d/web", "ns/app", "rs/web-1"].sort(),
    );
    expect(ids(visibleTree(NODES, next))).toContain("p/web-1-a");
  });

  it("opens the group holding a folded target", () => {
    const next = reveal(NODES, state(), "cm/b", false);
    expect(next.expandedGroups.has("__kg__ns/app__ConfigMap")).toBe(true);
    expect(ids(visibleTree(NODES, next))).toContain("cm/b");
  });

  it("raises the root when the target sits above it", () => {
    const at = state({ rootId: "rs/web-1" });
    const next = reveal(NODES, at, "ns/other", false);
    expect(next.rootId).toBe("cluster");
    expect(ids(visibleTree(NODES, next))).toContain("ns/other");
  });
});

describe("prune", () => {
  // A re-discovery keeps the structure but churns Pod names.
  const nextSnapshot = NODES.filter((x) => x.id !== "p/web-1-a").concat(
    n("p/web-1-z", "Pod", "web-1-z", "rs/web-1"),
  );

  it("drops ids the new snapshot no longer has", () => {
    const before = state({ expanded: new Set(["cluster", "ns/app", "p/web-1-a"]) });
    const { state: after } = prune(NODES, nextSnapshot, before, null);
    expect(after.expanded.has("p/web-1-a")).toBe(false);
    expect(after.expanded.has("ns/app")).toBe(true);
  });

  it("keeps a selection that survived and clears one that did not", () => {
    expect(prune(NODES, nextSnapshot, state(), "ns/app").selectedId).toBe("ns/app");
    expect(prune(NODES, nextSnapshot, state(), "p/web-1-a").selectedId).toBeNull();
  });

  it("falls back to the nearest surviving ancestor when the root dies", () => {
    const before = state({ rootId: "p/web-1-a" });
    expect(prune(NODES, nextSnapshot, before, null).state.rootId).toBe("rs/web-1");
  });

  it("falls back to the snapshot root when the whole chain dies", () => {
    const orphaned = [n("cluster", "Cluster", "dev/ci1")];
    const before = state({ rootId: "p/web-1-a" });
    expect(prune(NODES, orphaned, before, null).state.rootId).toBe("cluster");
  });
});

describe("resolveEdges", () => {
  // Arrows answer "what is this card wired to", so they hang off the selection.
  const SEL = "p/web-1-a";
  const e = (id: string, source: string, target: string, kind = "mounts"): GraphEdge => ({
    id,
    source,
    target,
    kind,
  });
  const pair = (r: { source: string; target: string }) => `${r.source}->${r.target}`;

  it("re-points a hidden endpoint to the group card standing in for it", () => {
    const st = state({ expanded: new Set(["cluster", "ns/app", "d/web", "rs/web-1"]) });
    const v = visibleTree(NODES, st);
    const r = resolveEdges(NODES, [e("e1", "p/web-1-a", "cm/a")], v, SEL);
    expect(r.map(pair)).toEqual(["p/web-1-a->__kg__ns/app__ConfigMap"]);
  });

  it("re-points a hidden endpoint to its nearest visible ancestor", () => {
    // The Pod is not on screen; the Deployment is the nearest thing that is.
    const st = state({ expanded: new Set(["cluster", "ns/app"]) });
    const v = visibleTree(NODES, st);
    const r = resolveEdges(NODES, [e("e1", "p/web-1-a", "svc/web")], v, SEL);
    expect(r.map(pair)).toEqual(["d/web->svc/web"]);
  });

  it("merges edges that collapse onto the same pair, carrying a count", () => {
    const st = state({ expanded: new Set(["cluster", "ns/app", "d/web", "rs/web-1"]) });
    const v = visibleTree(NODES, st);
    const r = resolveEdges(NODES, [e("e1", "p/web-1-a", "cm/a"), e("e2", "p/web-1-a", "cm/b")], v, SEL);
    expect(r).toHaveLength(1);
    expect(r[0].count).toBe(2);
  });

  it("snaps to the real member once the group is expanded", () => {
    const st = state({
      expanded: new Set(["cluster", "ns/app", "d/web", "rs/web-1"]),
      expandedGroups: new Set(["__kg__ns/app__ConfigMap"]),
    });
    const v = visibleTree(NODES, st);
    const r = resolveEdges(NODES, [e("e1", "p/web-1-a", "cm/a")], v, SEL);
    expect(r.map(pair)).toEqual(["p/web-1-a->cm/a"]);
  });

  it("drops an edge whose endpoints resolve to the same card", () => {
    const st = state({ expanded: new Set(["cluster", "ns/app"]) });
    const v = visibleTree(NODES, st);
    // Both the ReplicaSet and the Pod stand in as the Deployment.
    expect(resolveEdges(NODES, [e("e1", "rs/web-1", "p/web-1-a")], v, SEL)).toHaveLength(0);
  });

  it("drops an edge to something outside the visible root's subtree", () => {
    const st = state({ rootId: "ns/app", expanded: new Set(["ns/app"]) });
    const v = visibleTree(NODES, st);
    // Selected, so it is not filtered by scope — it is dropped because the
    // other end has no visible stand-in at all.
    expect(
      resolveEdges(NODES, [e("e1", "svc/web", "ns/other")], v, "svc/web"),
    ).toHaveLength(0);
  });

  it("keeps a single edge uncounted", () => {
    const st = state({ expanded: new Set(["cluster", "ns/app"]) });
    const v = visibleTree(NODES, st);
    const r = resolveEdges(NODES, [e("e1", "d/web", "svc/web")], v, "d/web");
    expect(r[0].count).toBe(1);
    expect(r[0].kind).toBe("mounts");
  });
});

describe("resolveEdges scoping", () => {
  const e = (id: string, source: string, target: string, kind = "mounts") => ({
    id,
    source,
    target,
    kind,
  });
  const open = state({ expanded: new Set(["cluster", "ns/app", "d/web", "rs/web-1"]) });

  it("draws nothing when nothing is selected", () => {
    const v = visibleTree(NODES, open);
    expect(resolveEdges(NODES, [e("e1", "p/web-1-a", "svc/web")], v, null)).toHaveLength(0);
  });

  it("draws only the edges touching the selected card", () => {
    const v = visibleTree(NODES, open);
    const r = resolveEdges(
      NODES,
      [
        e("e1", "p/web-1-a", "svc/web"),
        e("e2", "d/web", "svc/web"), // nothing to do with the selection
      ],
      v,
      "p/web-1-a",
    );
    expect(r.map((x) => x.id)).toHaveLength(1);
    expect(r[0].source).toBe("p/web-1-a");
  });

  it("follows the selection in either direction", () => {
    const v = visibleTree(NODES, open);
    const r = resolveEdges(NODES, [e("e1", "svc/web", "p/web-1-a", "selects")], v, "p/web-1-a");
    expect(r).toHaveLength(1);
  });

  // The card already carries a mark for its controller and its own Kind, so
  // repeating either as an arrow is clutter that says nothing new.
  it("never draws managed-by or instance-of", () => {
    const v = visibleTree(NODES, open);
    const r = resolveEdges(
      NODES,
      [
        e("e1", "p/web-1-a", "svc/web", "managed-by"),
        e("e2", "p/web-1-a", "svc/web", "instance-of"),
        e("e3", "p/web-1-a", "svc/web", "mounts"),
      ],
      v,
      "p/web-1-a",
    );
    expect(r.map((x) => x.kind)).toEqual(["mounts"]);
  });
});

describe("controllerMarks", () => {
  it("names the controller that manages a node", () => {
    const marks = controllerMarks(NODES, [
      { id: "m1", source: "d/web", target: "hr/web", kind: "managed-by" },
    ]);
    expect(marks.get("d/web")).toBe("HelmRelease");
  });

  it("ignores edges that are not management", () => {
    const marks = controllerMarks(NODES, [
      { id: "m1", source: "d/web", target: "svc/web", kind: "mounts" },
    ]);
    expect(marks.size).toBe(0);
  });
});
