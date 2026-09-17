import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  MiniMap,
  Panel,
  ReactFlow,
  getSmoothStepPath,
  useOnViewportChange,
  useReactFlow,
  type Edge as FlowEdge,
  type EdgeProps,
  type Node as FlowNode,
  type Viewport,
} from "@xyflow/react";
import dagre from "@dagrejs/dagre";
import { DESKTOP_EVENTS, onDesktopEvent } from "../lib/desktop";
import type { GraphNode } from "../types/graph";
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
} from "../lib/display";
import { absolutePositions, anchoredViewport, type Point } from "../lib/viewport";
import type { ResolvedEdge, Visible } from "../lib/tree";

interface Props {
  /** Exactly what is on screen, from lib/tree.ts. */
  visible: Visible;
  /** Relationship edges already resolved to visible cards. */
  relationships: ResolvedEdge[];
  /** Nodes whose children are shown — drives the ▸/▾ glyph. */
  expanded: Set<string>;
  /** id → number of containment children, for the toggle badge. */
  childCounts: Map<string, number>;
  /** Whether the visible root has a parent to climb to. */
  canShowParent: boolean;
  /** Name of the parent, for the climb control's label. */
  parentLabel?: string;
  selectedId: string | null;
  solo: boolean;
  onToggleSolo: () => void;
  onToggleExpand: (id: string) => void;
  onToggleGroup: (gid: string) => void;
  onShowParent: () => void;
  /** Bumped when something outside the canvas revealed a node; re-frames. */
  revealTick: number;
  onSelect: (node: GraphNode | null) => void;
}

const NODE_W = 200;
const NODE_H = 56;

// Org-chart spacing. Generous on purpose: the old 30/50 was tuned to cram a
// 40-node budget onto one screen, and that budget is gone — expansion is the
// user's now, so the canvas may be as large as what they opened.
const NODE_SEP = 44;
const RANK_SEP = 88;

// Dwell time before the hover tooltip (full untruncated name) appears.
const HOVER_DELAY_MS = 1500;

interface Layout {
  flowNodes: FlowNode[];
  flowEdges: FlowEdge[];
}

// Top-down org chart over exactly what `visibleTree` says is on screen.
//
// There is no second opinion about visibility here: this function lays out the
// nodes and group cards it is handed and draws the edges it is handed. Anything
// that decides what to show lives in lib/tree.ts, so one set of state governs
// the picture.
function layout(
  visible: Visible,
  relationships: ResolvedEdge[],
  childCounts: Map<string, number>,
  expanded: Set<string>,
  selectedId: string | null,
): Layout {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", nodesep: NODE_SEP, ranksep: RANK_SEP });
  g.setDefaultEdgeLabel(() => ({}));

  for (const n of visible.nodes) g.setNode(n.id, { width: NODE_W, height: NODE_H });
  for (const gr of visible.groups) g.setNode(gr.id, { width: NODE_W, height: NODE_H });
  for (const e of visible.containment) {
    // Infra containment ranks child→parent so the control-plane hangs above the
    // cluster node rather than looping around it.
    const child = visible.nodes.find((n) => n.id === e.target);
    const flip = child && INFRA_KINDS.has(child.kind);
    g.setEdge(flip ? e.target : e.source, flip ? e.source : e.target);
  }
  dagre.layout(g);

  const at = (id: string) => {
    const p = g.node(id);
    return p ? { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 } : null;
  };

  const flowNodes: FlowNode[] = [];

  for (const n of visible.nodes) {
    const p = at(n.id);
    if (!p) continue;
    const hex = HEALTH_HEX[health(n)];
    const isSelected = n.id === selectedId;
    const kids = childCounts.get(n.id) ?? 0;
    const isOpen = expanded.has(n.id);
    flowNodes.push({
      id: n.id,
      position: p,
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
            {kids > 0 && (
              <button
                type="button"
                data-toggle={n.id}
                aria-label={`${isOpen ? "Collapse" : "Expand"} ${n.name}`}
                title={
                  isOpen
                    ? `Collapse — hides ${kids} inside`
                    : `Expand — ${kids} inside`
                }
                className="nodrag shrink-0 rounded-full bg-slate-200 px-1.5 text-[10px] font-medium text-slate-600 hover:bg-slate-300"
              >
                {isOpen ? "▾" : "▸"} {kids}
              </button>
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
    });
  }

  for (const gr of visible.groups) {
    const p = at(gr.id);
    if (!p) continue;
    flowNodes.push({
      id: gr.id,
      position: p,
      data: {
        label: (
          <div className="flex w-full items-center gap-2 text-left">
            <span
              className={`shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold ${kindChipClass(gr.kind)}`}
            >
              {kindAbbrev(gr.kind)}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-xs font-medium text-slate-900">
                {kindPlural(gr.kind)}
              </span>
              <span className="block text-[10px] text-slate-400">
                {gr.expanded ? "click to collapse" : "click to expand"}
              </span>
            </span>
            <span className="shrink-0 rounded-full bg-slate-200 px-1.5 text-[10px] font-medium text-slate-600">
              {gr.expanded ? "▾" : "▸"} {gr.memberIds.length}
            </span>
          </div>
        ),
        groupToggle: gr.id,
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
  }

  const flowEdges: FlowEdge[] = [];
  for (const e of visible.containment) {
    const child = visible.nodes.find((n) => n.id === e.target);
    const flip = child && INFRA_KINDS.has(child.kind);
    flowEdges.push({
      id: e.id,
      source: flip ? e.target : e.source,
      target: flip ? e.source : e.target,
      style: { stroke: "#cbd5e1" },
    });
  }

  // One caption per (source, kind), pinned under the source card (see
  // RelationshipEdge). A fan-out — one Service selecting five Pods — reads as a
  // single "selects" under the Service, not five copies scattered along the
  // lines; a fan-in — five resources managed by one Kustomization — captions
  // each source card individually.
  const captioned = new Set<string>();
  const captionSlots = new Map<string, number>();
  for (const e of relationships) {
    const rel = EDGE_STYLE[e.kind];
    let caption: Pick<FlowEdge, "label" | "data"> | undefined;
    if (!captioned.has(`${e.source}|${e.kind}`)) {
      captioned.add(`${e.source}|${e.kind}`);
      const slot = captionSlots.get(e.source) ?? 0;
      captionSlots.set(e.source, slot + 1);
      // A merged edge says how many underlying links it stands for, so a
      // collapsed group never understates the wiring behind it.
      caption = {
        label: e.count > 1 ? `${e.kind} ×${e.count}` : e.kind,
        data: { labelSlot: slot },
      };
    }
    flowEdges.push({
      id: e.id,
      source: e.source,
      target: e.target,
      ...caption,
      type: "rel",
      style: rel
        ? { stroke: rel.stroke, strokeDasharray: "6 3" }
        : { stroke: "#cbd5e1" },
    });
  }

  return { flowNodes, flowEdges };
}

// Relationship edge with a pinned caption. The default edge label sits at the
// path midpoint as bare SVG text in the same paint layer as every edge path —
// which failed two ways in a converging graph: lines drawn later struck the
// text through, and nothing said which line (or node) the caption belonged to.
// This fixes both by construction. <EdgeLabelRenderer> is an HTML layer stacked
// above ALL edge paths, so no line can ever cross a chip; and the chip hangs
// directly under the edge's SOURCE card — captions name what the source does
// ("api managed-by …", "gateway selects …"), so that is the node they must
// visually attach to. Several captions on one card stack downward.
const CAPTION_GAP = 10; // px between the card's bottom edge and its first chip
const CAPTION_STEP = 17; // px between stacked chips

function RelationshipEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style,
  label,
  data,
}: EdgeProps) {
  // Orthogonal routing — bezier curves between same-rank siblings loop
  // unpleasantly.
  const [path] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  if (label == null) return <BaseEdge id={id} path={path} style={style} />;
  const slot = (data as { labelSlot?: number } | undefined)?.labelSlot ?? 0;
  const color = (style?.stroke as string) ?? "#64748b";
  return (
    <>
      <BaseEdge id={id} path={path} style={style} />
      <EdgeLabelRenderer>
        <div
          style={{
            position: "absolute",
            // (sourceX, sourceY) is the bottom-centre handle the line leaves
            // from, so the chip sits threaded onto its own edge's first segment.
            transform: `translate(-50%, 0) translate(${sourceX}px, ${sourceY + CAPTION_GAP + slot * CAPTION_STEP}px)`,
            pointerEvents: "none",
            background: "#fff",
            border: `1px solid ${color}`,
            color,
            borderRadius: 8,
            padding: "0 5px",
            fontSize: 9,
            fontWeight: 600,
            lineHeight: "13px",
            whiteSpace: "nowrap",
          }}
        >
          {label}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}

// Module-level so the mapping keeps one identity across renders — xyflow warns
// and re-creates all edges when it changes.
const EDGE_TYPES = { rel: RelationshipEdge };

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
  /** The toggled card's absolute position *before* the reflow. */
  pos: Point;
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
  visible,
  relationships,
  expanded,
  childCounts,
  canShowParent,
  parentLabel,
  selectedId,
  solo,
  onToggleSolo,
  onToggleExpand,
  onToggleGroup,
  onShowParent,
  revealTick,
  onSelect,
}: Props) {
  const { flowNodes, flowEdges } = useMemo(
    () => layout(visible, relationships, childCounts, expanded, selectedId),
    [visible, relationships, childCounts, expanded, selectedId],
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
  // A different visible root is a different view (k9s handoff, ?focus=,
  // "show parent"), so refit. Selection deliberately does NOT remount any more:
  // remounting on every click is exactly how the old model threw away the
  // user's place.
  const rootId = visible.nodes[0]?.id ?? null;
  const [seen, setSeen] = useState({ rootId, revealTick });
  if (rootId !== seen.rootId || revealTick !== seen.revealTick) {
    setSeen({ rootId, revealTick });
    setViewKey((k) => k + 1);
    if (anchor) setAnchor(null);
    if (tip) setTip(null);
  }
  // Selection still dismisses the tooltip; it just doesn't move the graph.
  const [seenSelectedId, setSeenSelectedId] = useState(selectedId);
  if (selectedId !== seenSelectedId) {
    setSeenSelectedId(selectedId);
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
        edgeTypes={EDGE_TYPES}
        fitView
        // Whole-cluster views are wide; the default minZoom (0.5) would stop
        // fitView from actually fitting them.
        minZoom={0.04}
        maxZoom={1.25}
        // kscope is a viewer: positions are computed, so dragging nodes is a
        // foot-gun — the big translucent group containers read as background,
        // and "panning" on one flings the entire grid off-screen.
        nodesDraggable={false}
        onNodeClick={(event, n) => {
          // Dismiss on click even when nothing moves — the layout may still
          // shift under the tooltip.
          clearTip();
          const d = n.data as { raw?: GraphNode; groupToggle?: string };
          // Where the clicked card is right now — the position to hold across
          // the reflow so expanding never teleports the thing you clicked.
          const here = absPos.get(n.id) ?? null;
          if (d.groupToggle) {
            if (here) setAnchor({ id: d.groupToggle, pos: here });
            onToggleGroup(d.groupToggle);
            return;
          }
          if (!d.raw) return;
          // One card, two targets: the ▸/▾ badge is structure, everywhere else
          // is details. Keeping them apart is what lets selection stop
          // reshaping the tree.
          const hitToggle = (event.target as HTMLElement | null)?.closest?.(
            "[data-toggle]",
          );
          if (hitToggle) {
            if (here) setAnchor({ id: d.raw.id, pos: here });
            onToggleExpand(d.raw.id);
            return;
          }
          onSelect(d.raw);
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
        <Panel position="top-left" className="flex items-center gap-2">
          {canShowParent && (
            <button
              type="button"
              onClick={onShowParent}
              title="Bring the parent into view, keeping this branch open"
              className="rounded-md border border-dashed border-slate-300 bg-slate-50 px-2.5 py-1.5 text-xs font-medium text-slate-600 shadow-sm hover:bg-white"
            >
              ⌃ show parent{parentLabel ? ` · ${parentLabel}` : ""}
            </button>
          )}
          <label
            title="While on, expanding a node collapses its siblings"
            className="flex cursor-pointer items-center gap-1.5 rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-xs font-medium text-slate-600 shadow-sm"
          >
            <input
              type="checkbox"
              checked={solo}
              onChange={onToggleSolo}
              className="h-3 w-3 accent-blue-500"
            />
            one branch at a time
          </label>
        </Panel>
        <RecenterButton />
        <AnchorKeeper anchor={anchor} absPos={absPos} onApplied={clearAnchor} />
        {flowNodes.length > 15 && <MiniMap pannable zoomable />}
      </ReactFlow>
    </div>
  );
}
