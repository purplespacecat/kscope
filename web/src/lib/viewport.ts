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
 * The id a clicked kind-group card will have after the toggle. Expanding
 * replaces the collapsed card `gid` with a container `gid` plus a header card
 * `gid__h`; anchoring on `gid` would latch onto the container's corner.
 */
export function groupAnchorId(gid: string, willExpand: boolean): string {
  return willExpand ? `${gid}${HEADER_SUFFIX}` : gid;
}

/**
 * Whether clicking the card with id `clickedId` expands or collapses group
 * `gid`. A collapsed card's id *is* `gid`; an expanded group's header card is
 * `gid__h`. Read from the clicked node rather than from the expanded-group set
 * so a rapid second click can't act on a render that hasn't caught up.
 */
export function groupWillExpand(clickedId: string, gid: string): boolean {
  return clickedId === gid;
}

/**
 * Absolute positions for every node. Container members carry positions
 * relative to their parent (`parentId` + `extent: "parent"`), so raw values
 * aren't comparable across a reflow.
 */
export function absolutePositions(nodes: Placed[]): Map<string, Point> {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const abs = new Map<string, Point>();

  const resolve = (n: Placed): Point => {
    const done = abs.get(n.id);
    if (done) return done;
    const parent = n.parentId ? byId.get(n.parentId) : undefined;
    // A dangling parentId falls back to the raw position: failing to anchor
    // beats NaN coordinates, which would corrupt the viewport instead.
    const base = parent ? resolve(parent) : { x: 0, y: 0 };
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
