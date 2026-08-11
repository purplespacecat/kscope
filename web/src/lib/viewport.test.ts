import { describe, expect, it } from "vitest";
import {
  absolutePositions,
  anchoredViewport,
  groupAnchorId,
  groupWillExpand,
  type Point,
  type ViewportRect,
} from "./viewport";

// Where a flow-coordinate point lands on screen. The whole point of anchoring
// is that this value is unchanged across a reflow, so the tests assert the
// invariant rather than just the arithmetic.
const screenPos = (p: Point, vp: ViewportRect) => ({
  x: p.x * vp.zoom + vp.x,
  y: p.y * vp.zoom + vp.y,
});

describe("anchoredViewport", () => {
  it("holds the node's screen position when it moves", () => {
    const prev = { x: 100, y: 200 };
    const next = { x: 140, y: 260 };
    const vp = { x: 0, y: 0, zoom: 1 };

    const out = anchoredViewport(prev, next, vp);

    expect(screenPos(next, out)).toEqual(screenPos(prev, vp));
  });

  it("scales the correction by zoom", () => {
    // A half-zoomed view must pan by half the layout delta — forgetting the
    // zoom factor is the obvious way to get this wrong, and it only shows up
    // when the user isn't at 100%.
    const prev = { x: 100, y: 100 };
    const next = { x: 200, y: 300 };
    const vp = { x: 10, y: 20, zoom: 0.5 };

    const out = anchoredViewport(prev, next, vp);

    expect(out).toEqual({ x: -40, y: -80, zoom: 0.5 });
    expect(screenPos(next, out)).toEqual(screenPos(prev, vp));
  });

  it("leaves the viewport untouched when the node did not move", () => {
    const vp = { x: 33, y: 44, zoom: 0.8 };
    expect(anchoredViewport({ x: 5, y: 6 }, { x: 5, y: 6 }, vp)).toEqual(vp);
  });

  it("never changes zoom", () => {
    const out = anchoredViewport({ x: 0, y: 0 }, { x: 999, y: -999 }, {
      x: 0,
      y: 0,
      zoom: 0.04,
    });
    expect(out.zoom).toBe(0.04);
  });
});

describe("groupAnchorId", () => {
  // Expanding a group card doesn't move it, it replaces it: the collapsed
  // card `gid` becomes a container `gid` plus a header card `gid__h`.
  // Anchoring on `gid` would latch onto the container's corner instead.
  it("anchors on the expanded header when expanding", () => {
    expect(groupAnchorId("__kg__ns/web__Pod", true)).toBe("__kg__ns/web__Pod__h");
  });

  it("anchors on the collapsed card when collapsing", () => {
    expect(groupAnchorId("__kg__ns/web__Pod", false)).toBe("__kg__ns/web__Pod");
  });
});

describe("groupWillExpand", () => {
  // Derived from the clicked card's own id rather than from the expandedGroups
  // set: a fast double-click would otherwise read state from a render that
  // hasn't caught up yet and expand twice instead of toggling back.
  it("expands when the collapsed card itself was clicked", () => {
    expect(groupWillExpand("__kg__ns/web__Pod", "__kg__ns/web__Pod")).toBe(true);
  });

  it("collapses when the expanded header was clicked", () => {
    expect(groupWillExpand("__kg__ns/web__Pod__h", "__kg__ns/web__Pod")).toBe(false);
  });
});

describe("absolutePositions", () => {
  it("passes through nodes that have no parent", () => {
    const abs = absolutePositions([{ id: "a", position: { x: 5, y: 7 } }]);
    expect(abs.get("a")).toEqual({ x: 5, y: 7 });
  });

  it("offsets container members by the container origin", () => {
    // Members carry positions relative to their parent (parentId + extent:
    // "parent"), so raw positions aren't comparable across a reflow.
    const abs = absolutePositions([
      { id: "box", position: { x: 1000, y: 500 } },
      { id: "pod", position: { x: 16, y: 16 }, parentId: "box" },
    ]);
    expect(abs.get("pod")).toEqual({ x: 1016, y: 516 });
  });

  it("accumulates through a nested parent chain", () => {
    const abs = absolutePositions([
      { id: "outer", position: { x: 100, y: 100 } },
      { id: "inner", position: { x: 10, y: 20 }, parentId: "outer" },
      { id: "leaf", position: { x: 1, y: 2 }, parentId: "inner" },
    ]);
    expect(abs.get("leaf")).toEqual({ x: 111, y: 122 });
  });

  it("falls back to the raw position when the parent is missing", () => {
    // Defensive: a dangling parentId must not produce NaN coordinates, which
    // would silently corrupt the viewport instead of just failing to anchor.
    const abs = absolutePositions([
      { id: "orphan", position: { x: 3, y: 4 }, parentId: "gone" },
    ]);
    expect(abs.get("orphan")).toEqual({ x: 3, y: 4 });
  });
});
