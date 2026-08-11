import { describe, expect, it } from "vitest";
import {
  absolutePositions,
  anchoredViewport,
  firstResolved,
  groupAnchorIds,
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

describe("groupAnchorIds", () => {
  // Toggling a group replaces nodes rather than moving them, and which id
  // survives depends on the direction. Rather than predict the direction at
  // click time — which misreads a click on the expanded container, whose id is
  // also `gid` — both candidates are offered and whichever exists afterwards
  // wins: expanded has `gid__h` (header) plus `gid` (container), collapsed has
  // only `gid` (card).
  it("prefers the header card over the bare group id", () => {
    expect(groupAnchorIds("__kg__ns/web__Pod")).toEqual([
      "__kg__ns/web__Pod__h",
      "__kg__ns/web__Pod",
    ]);
  });
});

describe("firstResolved", () => {
  const abs = new Map<string, Point>([
    ["a", { x: 1, y: 2 }],
    ["b", { x: 3, y: 4 }],
  ]);

  it("takes the first candidate that exists", () => {
    expect(firstResolved(abs, ["missing", "b", "a"])).toEqual({ x: 3, y: 4 });
  });

  it("returns undefined when nothing resolves", () => {
    // The caller leaves the viewport untouched in this case — failing to anchor
    // beats panning to an arbitrary place.
    expect(firstResolved(abs, ["missing", "gone"])).toBeUndefined();
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

  it("gives every node on a parent cycle its raw position", () => {
    // Unreachable from today's layout(), but the dangling-parent case is
    // defended, and an unguarded chain walk would take the canvas down with a
    // stack overflow rather than merely failing to anchor.
    const abs = absolutePositions([
      { id: "self", position: { x: 1, y: 1 }, parentId: "self" },
      { id: "ping", position: { x: 2, y: 2 }, parentId: "pong" },
      { id: "pong", position: { x: 3, y: 3 }, parentId: "ping" },
    ]);
    expect(abs.get("self")).toEqual({ x: 1, y: 1 });
    expect(abs.get("ping")).toEqual({ x: 2, y: 2 });
    expect(abs.get("pong")).toEqual({ x: 3, y: 3 });
  });

  it("resolves a cycle the same way whichever member is seen first", () => {
    // Breaking the walk wherever re-entry happens to be detected would make the
    // result depend on array order, and a pre-reflow position resolved through
    // one break point against a post-reflow position resolved through another
    // would pan the viewport by an arbitrary offset.
    const cycle = [
      { id: "ping", position: { x: 2, y: 2 }, parentId: "pong" },
      { id: "pong", position: { x: 3, y: 3 }, parentId: "ping" },
    ];
    const forward = absolutePositions(cycle);
    const reversed = absolutePositions([...cycle].reverse());

    expect(forward.get("ping")).toEqual(reversed.get("ping"));
    expect(forward.get("pong")).toEqual(reversed.get("pong"));
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
