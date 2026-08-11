// Geometry for keeping a clicked node anchored across a layout change.
// See docs/focus-anchoring.md.

export interface Point {
  x: number;
  y: number;
}

export interface ViewportRect extends Point {
  zoom: number;
}

/** The subset of a flow node this module needs: where it is, and inside what. */
export interface Placed {
  id: string;
  position: Point;
  parentId?: string;
}

/**
 * Suffix of an expanded kind-group's header card. Shared with the layout so
 * the two can't drift — anchoring depends on predicting this id exactly.
 */
export const HEADER_SUFFIX = "__h";

/**
 * Candidate ids for a kind-group's visual representative after a toggle, best
 * first. Toggling replaces nodes rather than moving them: expanded, the group is
 * a container `gid` plus a header card `gid__h`; collapsed, it is a single card
 * `gid`. Offering both candidates and taking whichever exists afterwards avoids
 * having to predict the direction at click time — a prediction that misreads a
 * click on the expanded container, whose id is also `gid`.
 */
export function groupAnchorIds(gid: string): string[] {
  return [`${gid}${HEADER_SUFFIX}`, gid];
}

/**
 * First candidate with a known position, or undefined if none resolve — in
 * which case the caller leaves the viewport alone. Failing to anchor beats
 * panning somewhere arbitrary.
 */
export function firstResolved(
  abs: Map<string, Point>,
  ids: string[],
): Point | undefined {
  for (const id of ids) {
    const p = abs.get(id);
    if (p) return p;
  }
  return undefined;
}

/**
 * Absolute positions for every node. Container members carry positions
 * relative to their parent (`parentId` + `extent: "parent"`), so raw values
 * aren't comparable across a reflow.
 */
export function absolutePositions(nodes: Placed[]): Map<string, Point> {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const abs = new Map<string, Point>();
  const walking = new Set<string>();

  const resolve = (n: Placed): Point => {
    const done = abs.get(n.id);
    if (done) return done;
    // A cycle contributes no offset, so the node keeps its raw position. Not
    // reachable from today's layout() — containers carry no parentId — but an
    // unguarded chain walk would overflow the stack and take the canvas down,
    // where a dangling parentId merely fails to anchor.
    if (walking.has(n.id)) return { x: 0, y: 0 };
    walking.add(n.id);
    const parent = n.parentId ? byId.get(n.parentId) : undefined;
    // A dangling parentId falls back to the raw position: failing to anchor
    // beats NaN coordinates, which would corrupt the viewport instead.
    const base = parent ? resolve(parent) : { x: 0, y: 0 };
    walking.delete(n.id);
    const p = { x: base.x + n.position.x, y: base.y + n.position.y };
    abs.set(n.id, p);
    return p;
  };

  for (const n of nodes) resolve(n);
  return abs;
}

/**
 * Pan `vp` so a node that moved from `prev` to `next` keeps the same screen
 * position. Screen position is `p * zoom + vp`, so holding it constant gives
 * `vp_new = vp_old - (next - prev) * zoom`. Zoom is never changed.
 */
export function anchoredViewport(
  prev: Point,
  next: Point,
  vp: ViewportRect,
): ViewportRect {
  return {
    x: vp.x - (next.x - prev.x) * vp.zoom,
    y: vp.y - (next.y - prev.y) * vp.zoom,
    zoom: vp.zoom,
  };
}
