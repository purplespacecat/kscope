import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  Controls,
  MiniMap,
  Panel,
  ReactFlow,
  useOnViewportChange,
  useReactFlow,
  type Edge as FlowEdge,
  type Node as FlowNode,
  type Viewport,
} from "@xyflow/react";
import dagre from "@dagrejs/dagre";
import { DESKTOP_EVENTS, onDesktopEvent } from "../lib/desktop";
import type { GraphEdge, GraphNode } from "../types/graph";
import {
  EDGE_STYLE,
  HEALTH_DOT,
  HEALTH_HEX,
  HEALTH_LABEL,
  INFRA_KINDS,
  health,
  kindAbbrev,
  kindChipClass,
  kindPlural,
  kindRank,
} from "../lib/display";
import { absolutePositions, anchoredViewport, type Point } from "../lib/viewport";

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

// Grid geometry for grouped/wrapped children.
const WRAP_AT = 6;
const GRID_GAP_X = 16;
const GRID_GAP_Y = 24;
const GRID_PAD = 16;

// A parent's leaf children of the same kind fold into an expandable
// kind-group once there are at least this many of them.
const GROUP_AT = 3;

// Namespaces are the primary drill path — never hide them behind a group.
const GROUP_EXEMPT = new Set(["Namespace"]);

// Suffix of an expanded group's members box. The group *card* keeps the bare id
// in both states, so a toggle never changes what the anchor is holding.
const MEMBER_BOX_SUFFIX = "__m";

// Dwell time before the hover tooltip (full untruncated name) appears.
const HOVER_DELAY_MS = 1500;

interface Layout {
  flowNodes: FlowNode[];
  flowEdges: FlowEdge[];
}

function gridSize(items: number): { w: number; h: number } {
  const cols = Math.min(WRAP_AT, items);
  const rows = Math.ceil(items / WRAP_AT);
  return {
    w: cols * NODE_W + (cols - 1) * GRID_GAP_X + 2 * GRID_PAD,
    h: rows * NODE_H + (rows - 1) * GRID_GAP_Y + 2 * GRID_PAD,
  };
}

function gridSlot(i: number, total: number, boxW: number): { x: number; y: number } {
  const row = Math.floor(i / WRAP_AT);
  const col = i % WRAP_AT;
  const inRow = Math.min(WRAP_AT, total - row * WRAP_AT);
  const rowLeft =
    GRID_PAD + (boxW - 2 * GRID_PAD - (inRow * NODE_W + (inRow - 1) * GRID_GAP_X)) / 2;
  return {
    x: rowLeft + col * (NODE_W + GRID_GAP_X),
    y: GRID_PAD + row * (NODE_H + GRID_GAP_Y),
  };
}

// Layered "iceberg" layout with kind-grouping:
//   - infra (control-plane, machines) ranks ABOVE the cluster node, content
//     below;
//   - a parent's leaf children fold by kind into expandable groups; the card
//     keeps its slot either way, and expanding adds a members-only box ranked
//     *below* it rather than a sibling box beside it;
//   - leftover ungrouped leaves beyond WRAP_AT wrap into a plain grid;
//   - relationship edges to packed members are dimmed, and hidden entirely
//     while their group is collapsed.
function layout(
  nodes: GraphNode[],
  edges: GraphEdge[],
  hiddenCounts: Map<string, number> | undefined,
  selectedId: string | null,
  expandedGroups: Set<string>,
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

  const byKindThenName = (a: string, b: string) => {
    const na = byId.get(a)!;
    const nb = byId.get(b)!;
    return kindRank(na.kind) - kindRank(nb.kind) || na.name.localeCompare(nb.name);
  };

  type Slot =
    | { t: "n"; id: string }
    | { t: "g"; gid: string; kind: string; count: number; expanded: boolean };
  interface Container {
    id: string;
    parent: string;
    members: Slot[];
    /** Group id this box belongs to; makes the box's background a collapse target. */
    toggle?: string;
    w: number;
    h: number;
  }
  const containers: Container[] = [];
  const groupCards: {
    id: string;
    parent: string;
    kind: string;
    count: number;
    expanded: boolean;
  }[] = [];
  const memberOf = new Map<string, string>(); // member id → container id
  const hiddenMembers = new Set<string>(); // members of collapsed groups
  const packed = new Set<string>(); // nodes not laid out by dagre directly

  for (const [parent, kids] of childIds) {
    const leaves = kids.filter((k) => {
      const n = byId.get(k);
      return n && !hasChildren.has(k) && !INFRA_KINDS.has(n.kind);
    });
    if (leaves.length === 0) continue;

    const buckets = new Map<string, string[]>();
    for (const l of leaves) {
      const kind = byId.get(l)!.kind;
      const list = buckets.get(kind);
      if (list) list.push(l);
      else buckets.set(kind, [l]);
    }

    const singles: string[] = [];
    const groupsHere: {
      gid: string;
      kind: string;
      count: number;
      expanded: boolean;
    }[] = [];
    // Members of expanded groups, to be boxed once we know what stands in for
    // their card in the dagre graph (see below).
    const boxesHere: { gid: string; members: string[] }[] = [];
    for (const [kind, members] of buckets) {
      if (members.length < GROUP_AT || GROUP_EXEMPT.has(kind)) {
        singles.push(...members);
        continue;
      }
      members.sort(byKindThenName);
      const gid = `__kg__${parent}__${kind}`;
      for (const m of members) packed.add(m);
      const expanded = expandedGroups.has(gid);
      // The card keeps its slot in both states — expanding must not move what is
      // already on screen — so it stays in `mixed` either way.
      groupsHere.push({ gid, kind, count: members.length, expanded });
      if (expanded) boxesHere.push({ gid, members });
      else for (const m of members) hiddenMembers.add(m);
    }

    // Group cards and singleton leaves share one mixed grid — a wide row of
    // cards is the very thing being fixed.
    const mixed: Slot[] = [
      ...groupsHere.map(
        (c) =>
          ({
            t: "g",
            gid: c.gid,
            kind: c.kind,
            count: c.count,
            expanded: c.expanded,
          }) as Slot,
      ),
      ...singles.map((id) => ({ t: "n", id }) as Slot),
    ];
    const slotKey = (s: Slot) =>
      s.t === "n"
        ? { kind: byId.get(s.id)!.kind, name: byId.get(s.id)!.name }
        : { kind: s.kind, name: kindPlural(s.kind) };
    mixed.sort((a, b) => {
      const ka = slotKey(a);
      const kb = slotKey(b);
      return kindRank(ka.kind) - kindRank(kb.kind) || ka.name.localeCompare(kb.name);
    });

    // Whichever dagre node stands in for a group card: the grid that packs it, or
    // the card itself when the children didn't need a grid. Members boxes hang
    // below this, which is what keeps them out of the card's own rank.
    let cardStandIn: (gid: string) => string;

    if (mixed.length > WRAP_AT) {
      const gridId = `__grid__${parent}`;
      const { w, h } = gridSize(mixed.length);
      containers.push({ id: gridId, parent, members: mixed, w, h });
      for (const s of mixed) {
        if (s.t === "n") {
          packed.add(s.id);
          memberOf.set(s.id, gridId);
        } else {
          packed.add(s.gid);
        }
      }
      g.setNode(gridId, { width: w, height: h });
      g.setEdge(parent, gridId);
      cardStandIn = () => gridId;
    } else {
      for (const s of mixed) {
        if (s.t === "g") {
          groupCards.push({
            id: s.gid,
            parent,
            kind: s.kind,
            count: s.count,
            expanded: s.expanded,
          });
          g.setNode(s.gid, { width: NODE_W, height: NODE_H });
          g.setEdge(parent, s.gid);
        }
        // plain singles stay ordinary dagre children (not packed)
      }
      cardStandIn = (gid) => gid;
    }

    // An expanded group's members go in their own box, ranked *below* the card
    // rather than beside it. Ranking it as a sibling of the grid — which is what
    // this used to do — put a ~1300px box next to the grid and shoved everything
    // sideways off both viewport edges.
    for (const box of boxesHere) {
      const boxId = `${box.gid}${MEMBER_BOX_SUFFIX}`;
      const { w, h } = gridSize(box.members.length);
      containers.push({
        id: boxId,
        parent,
        members: box.members.map((id) => ({ t: "n", id }) as Slot),
        toggle: box.gid,
        w,
        h,
      });
      for (const m of box.members) memberOf.set(m, boxId);
      g.setNode(boxId, { width: w, height: h });
      g.setEdge(cardStandIn(box.gid), boxId);
    }
  }

  for (const n of nodes) {
    if (!packed.has(n.id)) g.setNode(n.id, { width: NODE_W, height: NODE_H });
  }

  // Containment drives the layout; infra edges are flipped so those subtrees
  // grow upward from the cluster.
  for (const e of contains) {
    if (packed.has(e.target)) continue; // grouped/gridded: via container edge
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
    if (packed.has(e.source) || packed.has(e.target)) continue;
    if (!anchored.has(e.source) || !anchored.has(e.target)) {
      g.setEdge(e.source, e.target);
    }
  }

  dagre.layout(g);

  const pos = new Map<string, { x: number; y: number }>();
  for (const n of nodes) {
    if (packed.has(n.id)) continue;
    const p = g.node(n.id);
    if (p) pos.set(n.id, { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 });
  }

  const flowNodes: FlowNode[] = [];

  const nodeCard = (
    n: GraphNode,
    p: { x: number; y: number },
    container?: string,
  ): FlowNode => {
    const hex = HEALTH_HEX[health(n)];
    const isSelected = n.id === selectedId;
    const hiddenKids = hiddenCounts?.get(n.id) ?? 0;
    return {
      id: n.id,
      position: p,
      ...(container ? { parentId: container, extent: "parent" as const } : {}),
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
        border: isSelected
          ? "2px solid #3b82f6"
          : `1px ${n.synthetic ? "dashed" : "solid"} #cbd5e1`,
        boxShadow: isSelected
          ? "0 0 0 3px rgba(59,130,246,0.25)"
          : `inset 3px 0 0 ${hex}`,
        background: "#fff",
        fontSize: 12,
      },
    };
  };

  const groupHeaderCard = (
    id: string,
    gid: string,
    kind: string,
    count: number,
    expanded: boolean,
    p: { x: number; y: number },
    container?: string,
  ): FlowNode => ({
    id,
    position: p,
    ...(container ? { parentId: container, extent: "parent" as const } : {}),
    data: {
      label: (
        <div className="flex w-full items-center gap-2 text-left">
          <span
            className={`shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold ${kindChipClass(kind)}`}
          >
            {kindAbbrev(kind)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-xs font-medium text-slate-900">
              {kindPlural(kind)}
            </span>
            <span className="block text-[10px] text-slate-400">
              {expanded ? "click to collapse" : "click to expand"}
            </span>
          </span>
          <span className="shrink-0 rounded-full bg-slate-200 px-1.5 text-[10px] font-medium text-slate-600">
            {count}
          </span>
          <span className="shrink-0 text-[10px] text-slate-400">
            {expanded ? "▾" : "▸"}
          </span>
        </div>
      ),
      groupToggle: gid,
    },
    style: {
      width: NODE_W,
      padding: 8,
      borderRadius: 8,
      border: "1px dashed #94a3b8",
      background: "#f8fafc",
      fontSize: 12,
    },
  });

  // Containers (expanded kind-groups + residual grids) and their members.
  for (const c of containers) {
    const ph = g.node(c.id);
    if (!ph) continue;
    flowNodes.push({
      id: c.id,
      position: { x: ph.x - c.w / 2, y: ph.y - c.h / 2 },
      data: c.toggle ? { groupToggle: c.toggle } : { label: null },
      style: {
        width: c.w,
        height: c.h,
        background: "rgba(148,163,184,0.07)",
        border: "1px dashed #e2e8f0",
        borderRadius: 12,
      },
    });
    const total = c.members.length;
    let slot = 0;
    for (const m of c.members) {
      const p = gridSlot(slot++, total, c.w);
      if (m.t === "n") {
        const n = byId.get(m.id);
        if (n) flowNodes.push(nodeCard(n, p, c.id));
      } else {
        flowNodes.push(
          groupHeaderCard(m.gid, m.gid, m.kind, m.count, m.expanded, p, c.id),
        );
      }
    }
  }

  // Kind-group cards that weren't packed into a grid.
  for (const cc of groupCards) {
    const p = g.node(cc.id);
    if (!p) continue;
    flowNodes.push(
      groupHeaderCard(cc.id, cc.id, cc.kind, cc.count, cc.expanded, {
        x: p.x - NODE_W / 2,
        y: p.y - NODE_H / 2,
      }),
    );
  }

  // Regular dagre-placed nodes.
  for (const n of nodes) {
    const p = pos.get(n.id);
    if (p) flowNodes.push(nodeCard(n, p));
  }

  const flowEdges: FlowEdge[] = [];
  for (const c of containers) {
    // A members box is drawn hanging off its card even though dagre ranked it
    // under whatever packs that card — the same layout-edge/rendered-edge split
    // infra containment already uses.
    const source = c.toggle ?? c.parent;
    flowEdges.push({
      id: `${source}->${c.id}`,
      source,
      target: c.id,
      style: { stroke: "#cbd5e1" },
    });
  }
  for (const cc of groupCards) {
    flowEdges.push({
      id: `${cc.parent}->${cc.id}`,
      source: cc.parent,
      target: cc.id,
      style: { stroke: "#cbd5e1" },
    });
  }
  for (const e of edges) {
    const rel = EDGE_STYLE[e.kind];
    if (e.kind === "contains" && packed.has(e.target)) continue; // via container edge
    // Members of collapsed groups aren't rendered — neither is their wiring.
    if (hiddenMembers.has(e.source) || hiddenMembers.has(e.target)) continue;
    // Wiring that touches packed members stays visible (orphans should LOOK
    // different from wired resources) but dimmed and unlabeled unless it
    // touches the selection.
    const dimmed =
      !!rel &&
      (memberOf.has(e.source) || memberOf.has(e.target)) &&
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
      // same-rank siblings loop unpleasantly.
      type: rel ? "smoothstep" : undefined,
      style: rel
        ? { stroke: rel.stroke, strokeDasharray: "6 3", opacity: dimmed ? 0.3 : 1 }
        : { stroke: "#cbd5e1" },
    });
  }

  return { flowNodes, flowEdges };
}

// Escape hatch for lost viewports: one click re-frames the whole graph.
// (Needs the ReactFlow context, hence a child component inside <ReactFlow>.)
//
// Only rendered once the view has actually left the fitted "home" position:
// the fitted viewport is captured just after mount, and every pan/zoom end is
// compared against it. Clicking Re-center re-fits and *adopts* the landing
// viewport as the new home — necessary because anchored selections change the
// graph's contents without remounting, which would otherwise leave `home`
// describing a subgraph that no longer exists and the button stuck visible. The
// component itself stays mounted so the desktop-menu subscription survives
// while the button is hidden.
function RecenterButton() {
  const { fitView, getViewport, getNodes } = useReactFlow();
  const [moved, setMoved] = useState(false);
  const home = useRef<Viewport | null>(null);

  // Capture the fitted viewport as "home". Double-rAF: the initial fitView
  // applies after nodes are measured, a frame or two past mount.
  useEffect(() => {
    let inner: number;
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => {
        home.current = getViewport();
      });
    });
    return () => {
      cancelAnimationFrame(outer);
      cancelAnimationFrame(inner);
    };
  }, [getViewport]);

  useOnViewportChange({
    onEnd: (vp) => {
      const h = home.current;
      if (!h) return;
      const atHome =
        Math.abs(vp.x - h.x) < 1 &&
        Math.abs(vp.y - h.y) < 1 &&
        Math.abs(vp.zoom - h.zoom) < 0.001;
      setMoved(!atHome);
    },
  });

  const recenter = useCallback(() => {
    // xyflow only settles fitView's promise once nodes are initialised, so with
    // an empty graph — reachable via Ctrl-0 or the View menu before discovery
    // has run — it never resolves at all. Bail out rather than leave a pending
    // continuation behind.
    if (getNodes().length === 0) return;
    // The promise resolves when the transition finishes, so its landing
    // viewport *is* the new home. Adopting it beats comparing against the old
    // one, which anchored selections leave describing a subgraph that no longer
    // exists — the comparison would never come back equal and the button would
    // never hide. Concurrent re-centers share one resolver, so they all adopt
    // the same settled viewport rather than a half-animated one.
    void fitView({ padding: 0.1, duration: 300 }).then(() => {
      home.current = getViewport();
      setMoved(false);
    });
  }, [fitView, getNodes, getViewport]);

  // The desktop View menu drives the same action. Subscribing here rather than
  // in App keeps it next to the only code that holds the ReactFlow context.
  useEffect(
    () => onDesktopEvent(DESKTOP_EVENTS.recenter, recenter),
    [recenter],
  );

  if (!moved) return null;
  return (
    <Panel position="top-center">
      <button
        type="button"
        onClick={recenter}
        className="rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-xs font-medium text-slate-600 shadow-sm hover:bg-slate-50"
        title="Fit the whole graph back into view"
      >
        ⌖ Re-center
      </button>
    </Panel>
  );
}

/** The node to keep put across a reflow, and where it sat before it. */
interface Anchor {
  /** Id to look for after the reflow. Stable for cards and resources alike. */
  id: string;
  /** The clicked node's absolute position *before* the reflow. */
  pos: Point;
  /**
   * The selection this anchor was captured for. A selection change that doesn't
   * match it came from somewhere else (k9s handoff, tree, ?focus=) and must
   * refit rather than anchor.
   */
  forSelection: string | null;
}

// Pans the viewport so the anchored node keeps its screen position once the new
// layout lands. Needs the ReactFlow context, hence a child inside <ReactFlow>
// (same reason as RecenterButton). No rAF dance: unlike fitView this doesn't
// wait on measurement — the coordinates come from our own layout.
function AnchorKeeper({
  anchor,
  absPos,
  onApplied,
}: {
  anchor: Anchor | null;
  absPos: Map<string, Point>;
  onApplied: () => void;
}) {
  const { getViewport, setViewport } = useReactFlow();
  // The pan is relative — it reads the current viewport and applies a delta — so
  // applying the same anchor twice doubles the correction. StrictMode
  // double-invokes mount effects, so guard on identity rather than trusting the
  // dependency array to fire exactly once.
  const applied = useRef<Anchor | null>(null);
  useEffect(() => {
    if (!anchor || applied.current === anchor) return;
    applied.current = anchor;
    const next = absPos.get(anchor.id);
    // Unresolvable anchor: leave the viewport alone rather than jump somewhere
    // arbitrary.
    if (next) setViewport(anchoredViewport(anchor.pos, next, getViewport()));
    onApplied();
  }, [anchor, absPos, getViewport, setViewport, onApplied]);
  return null;
}

export function GraphCanvas({
  nodes,
  edges,
  hiddenCounts,
  selectedId,
  onSelect,
}: Props) {
  // Expanded kind-groups, keyed `__kg__<parent>__<kind>` so state survives
  // refocusing between views.
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());

  const { flowNodes, flowEdges } = useMemo(
    () => layout(nodes, edges, hiddenCounts, selectedId, expandedGroups),
    [nodes, edges, hiddenCounts, selectedId, expandedGroups],
  );

  // Clicking in the canvas keeps the clicked node under the cursor: the anchor
  // is recorded at click time and applied once the new layout is in hand.
  const [anchor, setAnchor] = useState<Anchor | null>(null);
  const absPos = useMemo(() => absolutePositions(flowNodes), [flowNodes]);
  const clearAnchor = useCallback(() => setAnchor(null), []);

  // Remount key. Was `selectedId`, which reset the viewport on every selection
  // — the very thing that lost the clicked node. Now it only bumps for
  // selections arriving from outside the canvas (tree, details panel, k9s
  // handoff, ?focus= URL), where there is no screen position to preserve and
  // fitting the new subgraph is the right answer.
  const [viewKey, setViewKey] = useState(0);

  // Hover-dwell tooltip: linger on a node for HOVER_DELAY_MS and the full
  // (untruncated) identity appears — no click needed.
  const wrapRef = useRef<HTMLDivElement>(null);
  const tipTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mousePos = useRef({ x: 0, y: 0 });
  const [tip, setTip] = useState<{
    node: GraphNode;
    x: number;
    y: number;
    flip: boolean;
  } | null>(null);

  const clearTip = () => {
    if (tipTimer.current) clearTimeout(tipTimer.current);
    tipTimer.current = null;
    setTip(null);
  };
  useEffect(() => clearTip, []); // clear pending timer on unmount

  // Any view change must dismiss the tooltip immediately. A selection from
  // outside the canvas remounts <ReactFlow> (keyed on viewKey), which destroys
  // the node the mouse was over — so its onNodeMouseLeave never fires and the
  // tooltip would linger over the new view. Anchored selections don't remount,
  // but the layout still shifts under the tooltip, so both cases dismiss it.
  // Render-time adjustment instead of an effect, same pattern as ScopePanel.
  const [seenSelectedId, setSeenSelectedId] = useState(selectedId);
  if (selectedId !== seenSelectedId) {
    setSeenSelectedId(selectedId);
    // Anchor only when this exact selection is the one the anchor was captured
    // for. Anchor *presence* alone isn't enough: it lives until AnchorKeeper's
    // effect runs, and an out-of-band selection (the k9s handoff is an IPC
    // callback, not a discrete React event) could land in that window and be
    // mistaken for the click's own. A stale anchor is dropped so the remount
    // can't pan on top of fitView.
    if (!anchor || anchor.forSelection !== selectedId) {
      setViewKey((k) => k + 1);
      if (anchor) setAnchor(null);
    }
    if (tip) setTip(null);
  }
  // The dwell timer needs the same treatment: a timer scheduled in the old
  // view must not pop a ghost tooltip after navigation. Refs can't be written
  // during render, so the schedule-time selectedId is compared at fire time.
  const selectedRef = useRef(selectedId);
  useEffect(() => {
    selectedRef.current = selectedId;
  }, [selectedId]);

  const trackMouse = (e: React.MouseEvent) => {
    const r = wrapRef.current?.getBoundingClientRect();
    mousePos.current = { x: e.clientX - (r?.left ?? 0), y: e.clientY - (r?.top ?? 0) };
  };

  return (
    <div ref={wrapRef} className="relative h-full w-full">
      {tip && (
        <div
          className="pointer-events-none absolute z-50 max-w-xs rounded-lg bg-slate-900 px-3 py-2 shadow-xl"
          style={{
            left: tip.flip ? tip.x - 14 : tip.x + 14,
            top: tip.y + 14,
            transform: tip.flip ? "translateX(-100%)" : undefined,
          }}
        >
          <div className="flex items-center gap-1.5">
            <span
              className={`rounded px-1 text-[10px] font-semibold ${kindChipClass(tip.node.kind)}`}
            >
              {kindAbbrev(tip.node.kind)}
            </span>
            <span className="text-[10px] uppercase tracking-wide text-slate-400">
              {tip.node.kind}
            </span>
          </div>
          <div className="mt-1 break-all text-xs font-medium text-white">
            {tip.node.name}
          </div>
          {tip.node.namespace && (
            <div className="text-[10px] text-slate-400">ns/{tip.node.namespace}</div>
          )}
          <div className="mt-1 inline-flex items-center gap-1 text-[10px] text-slate-300">
            <span
              className={`h-1.5 w-1.5 rounded-full ${HEALTH_DOT[health(tip.node)]}`}
            />
            {HEALTH_LABEL[health(tip.node)]}
          </div>
        </div>
      )}
      <ReactFlow
        // Remount to let fitView re-frame — but only for selections from
        // outside the canvas. See viewKey above.
        key={viewKey}
        nodes={flowNodes}
        edges={flowEdges}
        fitView
        // Whole-cluster views are wide; the default minZoom (0.5) would stop
        // fitView from actually fitting them.
        minZoom={0.04}
        maxZoom={1.25}
        // kscope is a viewer: positions are computed, so dragging nodes is a
        // foot-gun — the big translucent group containers read as background,
        // and "panning" on one flings the entire grid off-screen.
        nodesDraggable={false}
        onNodeClick={(_, n) => {
          // Dismiss on click even when selectedId won't change (re-clicking
          // the selected node, toggling a group) — the layout still shifts
          // under the tooltip.
          clearTip();
          const d = n.data as { raw?: GraphNode; groupToggle?: string };
          // Where the clicked card is right now — the position to hold.
          const here = absPos.get(n.id) ?? null;
          if (d.groupToggle) {
            const gid = d.groupToggle;
            // Direction comes from the set itself, never from the clicked id.
            // The anchor holds the *card*, which keeps its id and its slot through
            // the toggle — so this is right whether the click landed on the card
            // or on the members box background, and `here` is deliberately unused
            // for groups.
            const cardPos = absPos.get(gid);
            if (cardPos) {
              setAnchor({ id: gid, pos: cardPos, forSelection: selectedId });
            }
            setExpandedGroups((prev) => {
              const next = new Set(prev);
              if (next.has(gid)) next.delete(gid);
              else next.add(gid);
              return next;
            });
            return;
          }
          if (d.raw) {
            // Resource ids are stable across the reflow, so the node itself is
            // the anchor. Batched with the selection change below.
            if (here) setAnchor({ id: d.raw.id, pos: here, forSelection: d.raw.id });
            onSelect(d.raw);
          }
        }}
        onNodeMouseEnter={(e, n) => {
          const raw = (n.data as { raw?: GraphNode }).raw;
          if (!raw) return; // group cards/containers have no single identity
          trackMouse(e);
          if (tipTimer.current) clearTimeout(tipTimer.current);
          tipTimer.current = setTimeout(() => {
            // The view changed while this timer was pending — the node this
            // tooltip describes is gone; don't pop a ghost.
            if (selectedRef.current !== selectedId) return;
            const { x, y } = mousePos.current;
            const width = wrapRef.current?.clientWidth ?? Infinity;
            setTip({ node: raw, x, y, flip: x > width - 340 });
          }, HOVER_DELAY_MS);
        }}
        onNodeMouseMove={trackMouse}
        onNodeMouseLeave={clearTip}
        onPaneClick={() => {
          clearTip();
          onSelect(null);
        }}
      >
        <Background />
        <Controls />
        <RecenterButton />
        <AnchorKeeper anchor={anchor} absPos={absPos} onApplied={clearAnchor} />
        {flowNodes.length > 15 && <MiniMap pannable zoomable />}
      </ReactFlow>
    </div>
  );
}
