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
import type { GraphNode } from "../types/graph";
import {
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
import type { Relation, Visible } from "../lib/tree";

interface Props {
  /** Exactly what is on screen, from lib/tree.ts. */
  visible: Visible;
  /** What the selected card is related to, for the legend. */
  relations: Relation[];
  /** Card id → outline colour for a related card. */
  outlines: Map<string, string>;
  /** Nodes whose children are shown — drives the ▸/▾ glyph. */
  expanded: Set<string>;
  /** id → number of containment children, for the toggle badge. */
  childCounts: Map<string, number>;
  /** id → Kind of whatever manages it, shown as a mark instead of an arrow. */
  marks: Map<string, string>;
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
  /** Bring a related resource onto the canvas without selecting it. */
  onGoToRelated: (id: string) => void;
  /** Card to flag briefly after a reveal; `tick` re-flags the same card. */
  spotlight: { id: string | null; tick: number };
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
  outlines: Map<string, string>,
  childCounts: Map<string, number>,
  marks: Map<string, string>,
  expanded: Set<string>,
  selectedId: string | null,
  flashId: string | null,
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
    const ring = outlines.get(n.id);
    const flashing = n.id === flashId;
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
            {(n.gitops || marks.has(n.id)) && (
              <span
                title={
                  n.gitops
                    ? `Managed by Flux ${n.gitops.kind} ${n.gitops.namespace}/${n.gitops.name}`
                    : `Managed by ${marks.get(n.id)}`
                }
                className="shrink-0 rounded bg-fuchsia-50 px-1 text-[9px] font-semibold text-fuchsia-600"
              >
                {n.gitops ? "flux" : marks.get(n.id)}
              </span>
            )}
            {kids > 0 && (
              <span
                data-toggle={n.id}
                aria-label={`${isOpen ? "Collapse" : "Expand"} ${n.name}`}
                title={isOpen ? `${kids} inside — click to collapse` : `${kids} inside — click to expand`}
                className="shrink-0 rounded-full bg-slate-200 px-1.5 text-[10px] font-medium text-slate-600"
              >
                {isOpen ? "▾" : "▸"} {kids}
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
        // Selection is blue; a card related to it is ringed in the colour its
        // relationship carries in the legend, which is the whole indicator now.
        // It has to carry across a wide canvas without being hunted for, so the
        // ring is thick, haloed and backed by a wash of the same colour — a
        // 1px tint beside a 1px grey reads as noise rather than as an answer.
        border: isSelected
          ? "2px solid #3b82f6"
          : ring
            ? `3px solid ${ring}`
            : `1px ${n.synthetic ? "dashed" : "solid"} #cbd5e1`,
        boxShadow: isSelected
          ? "0 0 0 3px rgba(59,130,246,0.25)"
          : ring
            ? `0 0 0 5px ${ring}33, 0 2px 8px ${ring}40`
            : `inset 3px 0 0 ${hex}`,
        background: ring ? `${ring}0f` : "#fff",
        // Deliberately `outline`, not border or box-shadow: those are already
        // carrying health, selection and relationship, and this has to read on
        // top of any of them without displacing what they say.
        outline: flashing ? "3px solid #f59e0b" : undefined,
        outlineOffset: flashing ? "3px" : undefined,
        fontSize: 12,
      },
    });
  }

  for (const gr of visible.groups) {
    const p = at(gr.id);
    if (!p) continue;
    const ring = outlines.get(gr.id);
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
            <span
              data-toggle={gr.id}
              aria-label={`${gr.expanded ? "Collapse" : "Expand"} ${kindPlural(gr.kind)}`}
              className="shrink-0 rounded-full bg-slate-200 px-1.5 text-[10px] font-medium text-slate-600"
            >
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
        border: ring ? `3px solid ${ring}` : "1px dashed #94a3b8",
        boxShadow: ring ? `0 0 0 5px ${ring}33, 0 2px 8px ${ring}40` : undefined,
        background: ring ? `${ring}0f` : "#f8fafc",
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
  relations,
  outlines,
  expanded,
  childCounts,
  marks,
  canShowParent,
  parentLabel,
  selectedId,
  solo,
  onToggleSolo,
  onToggleExpand,
  onToggleGroup,
  onShowParent,
  onGoToRelated,
  spotlight,
  revealTick,
  onSelect,
}: Props) {
  // The spotlight fades on its own: it answers "which one did I just click",
  // which stops being a question a second or two later. Keyed on the tick so
  // clicking the same resource twice flags it twice.
  //
  // Picked up during render rather than in an effect — setting state
  // synchronously in an effect cascades renders, and the tick already says
  // whether this is a new request. The effect only schedules the fade, where
  // the setState sits in a callback rather than the effect body.
  const [flash, setFlash] = useState<{ tick: number; id: string | null }>({
    tick: spotlight.tick,
    id: null,
  });
  if (flash.tick !== spotlight.tick) {
    setFlash({ tick: spotlight.tick, id: spotlight.id });
  }
  const flashId = flash.id;
  useEffect(() => {
    if (!flash.id) return;
    const t = setTimeout(() => setFlash((f) => ({ ...f, id: null })), 1800);
    return () => clearTimeout(t);
  }, [flash.id, flash.tick]);

  const { flowNodes, flowEdges } = useMemo(
    () => layout(visible, outlines, childCounts, marks, expanded, selectedId, flashId),
    [visible, outlines, childCounts, marks, expanded, selectedId, flashId],
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

  // Whether the relationship panel lists individual resources or just counts.
  const [listOpen, setListOpen] = useState(false);

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
          // Dismiss on click even when nothing moves — the layout may still
          // shift under the tooltip.
          clearTip();
          const d = n.data as { raw?: GraphNode; groupToggle?: string };
          // Where the clicked card is right now — the position to hold across
          // the reflow so toggling never teleports the thing you clicked.
          const here = absPos.get(n.id) ?? null;

          // One gesture: clicking a card toggles it, and the ▸/▾ badge is an
          // indicator rather than a second control. Anywhere on the card does
          // the same thing, so there is nothing to aim at and nothing to learn.
          if (d.groupToggle) {
            if (here) setAnchor({ id: d.groupToggle, pos: here });
            onToggleGroup(d.groupToggle);
            return;
          }
          if (!d.raw) return;

          onSelect(d.raw);
          // A card with nothing inside it has nothing to toggle. This guard is
          // not observable — expanding a childless node renders identically —
          // so no test pins it; it is here to keep meaningless ids out of
          // `expanded`, which prune and any future "collapse all" would inherit.
          if ((childCounts.get(d.raw.id) ?? 0) > 0) {
            if (here) setAnchor({ id: d.raw.id, pos: here });
            onToggleExpand(d.raw.id);
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
        {relations.length > 0 && (
          <Panel
            position="top-right"
            aria-label="Related to this"
            className="max-w-xs rounded-md border border-slate-200 bg-white/95 px-3 py-2 shadow-sm"
          >
            <div className="mb-1 flex items-center gap-2">
              <span className="text-[10px] font-semibold uppercase tracking-wide text-slate-400">
                related to this
              </span>
              <button
                type="button"
                onClick={() => setListOpen((v) => !v)}
                aria-label={listOpen ? "Hide related resources" : "Show related resources"}
                className="ml-auto rounded px-1 text-[10px] text-slate-400 hover:bg-slate-100 hover:text-slate-600"
              >
                {listOpen ? "▾" : "▸"}
              </button>
            </div>
            <ul className="space-y-1">
              {relations.map((r) => (
                <li key={`${r.kind}-${r.phrase}`}>
                  <div className="flex items-center gap-2 text-xs">
                    <span
                      aria-hidden
                      className="h-2.5 w-2.5 shrink-0 rounded-sm"
                      style={{ background: r.color }}
                    />
                    <span className="text-slate-700">{r.phrase}</span>
                    <span className="ml-auto text-slate-400">
                      {r.cardIds.length}
                      {r.count !== r.cardIds.length ? ` (${r.count})` : ""}
                    </span>
                  </div>
                  {listOpen && (
                    <ul className="ml-[18px] mt-0.5 space-y-0.5">
                      {r.items.map((it) => (
                        <li key={it.id}>
                          <button
                            type="button"
                            onClick={() => onGoToRelated(it.id)}
                            title={`${it.kind} ${it.name} — show it on the canvas`}
                            className="flex w-full items-center gap-1.5 rounded px-1 py-0.5 text-left text-[11px] text-slate-600 hover:bg-slate-100"
                          >
                            <span
                              className={`shrink-0 rounded px-1 text-[9px] font-semibold ${kindChipClass(it.kind)}`}
                            >
                              {kindAbbrev(it.kind)}
                            </span>
                            <span className="truncate">{it.name}</span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              ))}
            </ul>
          </Panel>
        )}
        <RecenterButton />
        <AnchorKeeper anchor={anchor} absPos={absPos} onApplied={clearAnchor} />
        {flowNodes.length > 15 && <MiniMap pannable zoomable />}
      </ReactFlow>
    </div>
  );
}
