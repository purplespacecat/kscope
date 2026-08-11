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

  for (const start of nodes) {
    if (abs.has(start.id)) continue;

    // Walk up to an ancestor whose absolute position is already known (or to the
    // top of the chain), then apply the chain downwards. Iterative rather than
    // recursive so no chain, however deep or malformed, can overflow the stack.
    const chain: Placed[] = [];
    const onPath = new Set<string>();
    let base: Point = { x: 0, y: 0 };
    let cur: Placed | undefined = start;
    let cyclic = false;

    while (cur) {
      const known = abs.get(cur.id);
      if (known) {
        base = known;
        break;
      }
      if (onPath.has(cur.id)) {
        cyclic = true;
        break;
      }
      onPath.add(cur.id);
      chain.push(cur);
      // A dangling parentId simply ends the walk, leaving the topmost node with
      // its raw position: failing to anchor beats NaN coordinates, which would
      // corrupt the viewport instead.
      cur = cur.parentId ? byId.get(cur.parentId) : undefined;
    }

    if (cyclic) {
      // Every node on the cycle keeps its raw position, so the outcome doesn't
      // depend on which member happened to be reached first — an order-dependent
      // break point would pan by an arbitrary offset when the pre- and
      // post-reflow positions resolved through different ones. Unreachable from
      // today's layout(), where containers carry no parentId.
      for (const n of chain) if (!abs.has(n.id)) abs.set(n.id, n.position);
      continue;
    }

    for (let i = chain.length - 1; i >= 0; i--) {
      base = { x: base.x + chain[i].position.x, y: base.y + chain[i].position.y };
      abs.set(chain[i].id, base);
    }
  }

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
