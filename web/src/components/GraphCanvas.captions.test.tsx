import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "../App";
import type { Snapshot } from "../types/graph";

// Same jsdom shims as GraphCanvas.anchor.test.tsx, with one deliberate
// difference: the ResizeObserver stub fires its callback ASYNCHRONOUSLY.
// xyflow's measurement handler bails out until the store knows its DOM node,
// which happens after `observe()` — a synchronous callback is simply dropped,
// nothing ever measures, and unmeasured nodes render no edges at all. The
// anchor tests keep the synchronous stub because their comments (and one
// assertion strategy) are written against the measurement-never-lands world;
// this file needs edges, so it gets the working timing instead.
//
// It also delivers that callback MORE THAN ONCE. xyflow's handler bails out
// when the store does not yet know the node, and a dropped delivery is gone for
// good: nothing measures, no edges render, and a waitFor for a caption then
// burns its whole budget waiting for something that will never arrive. One
// `setTimeout(…, 0)` wins that race on a fast machine and loses it on a loaded
// CI runner — the same commit passed at 08:30 and failed twice at 10:25 and
// 10:32 with "Unable to find an element with the text: managed-by". Re-delivery
// is also the faithful behaviour: a real ResizeObserver keeps reporting until
// it is disconnected, rather than announcing a box once and giving up. The
// attempts are bounded so a test can never hang on this stub.
class ResizeObserverStub {
  private cb: ResizeObserverCallback;
  private timers: ReturnType<typeof setTimeout>[] = [];
  constructor(cb: ResizeObserverCallback) {
    this.cb = cb;
  }
  observe(target: Element) {
    for (const delay of [0, 1, 5, 20, 50]) {
      this.timers.push(
        setTimeout(
          () => this.cb([{ target } as ResizeObserverEntry], this as unknown as ResizeObserver),
          delay,
        ),
      );
    }
  }
  unobserve() {
    this.disconnect();
  }
  disconnect() {
    for (const t of this.timers) clearTimeout(t);
    this.timers = [];
  }
}
globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;

class DOMMatrixReadOnlyStub {
  m22: number;
  constructor(transform?: string) {
    const scale = transform?.match(/scale\(([\d.]+)\)/)?.[1];
    this.m22 = scale === undefined ? 1 : +scale;
  }
}
globalThis.DOMMatrixReadOnly =
  DOMMatrixReadOnlyStub as unknown as typeof DOMMatrixReadOnly;

Object.defineProperties(globalThis.HTMLElement.prototype, {
  offsetWidth: { get(this: HTMLElement) { return parseFloat(this.style.width) || 1 } },
  offsetHeight: { get(this: HTMLElement) { return parseFloat(this.style.height) || 1 } },
});
(globalThis.SVGElement.prototype as unknown as { getBBox: () => DOMRect }).getBBox =
  () => ({ x: 0, y: 0, width: 0, height: 0 }) as DOMRect;

// Mirrors of GraphCanvas's own constants.
const NODE_W = 200;
const CAPTION_STEP = 17;

const NS = "core/namespace/web";
const DEP = "apps/deployment/web/api";
const DEP_B = "apps/deployment/web/worker";
const KUST = "kustomize/kustomization/web/apps";
const CM = "core/configmap/web/cfg";

// Two deployments managed by one Kustomization (fan-in: a caption per source
// card), plus two "uses" edges leaving ONE deployment (fan-out: a single
// caption). Two of each kind stays under GROUP_AT, so all three stay ordinary
// top-level cards whose geometry jsdom can measure.
const captionSnapshot: Snapshot = {
  scope: { context: "kind-dev", namespaces: ["web"] },
  timestamp: "2026-08-11T09:00:00Z",
  cluster: { context: "kind-dev", server: "https://x", version: "v1.33.0" },
  nodes: [
    { id: "cluster", kind: "Cluster", name: "kind-dev", health: "healthy", synthetic: true },
    { id: NS, kind: "Namespace", name: "web", parentId: "cluster", health: "healthy" },
    { id: DEP, kind: "Deployment", name: "api", parentId: NS, namespace: "web", health: "healthy" },
    { id: DEP_B, kind: "Deployment", name: "worker", parentId: NS, namespace: "web", health: "healthy" },
    { id: KUST, kind: "Kustomization", name: "apps", parentId: NS, namespace: "web", health: "healthy" },
    { id: CM, kind: "ConfigMap", name: "cfg", parentId: NS, namespace: "web", health: "healthy" },
  ],
  edges: [
    // managed-by must never become a line: the card carries a mark instead.
    { id: "e1", source: DEP, target: KUST, kind: "managed-by" },
    { id: "e2", source: DEP_B, target: KUST, kind: "managed-by" },
    // Two "uses" out of one card: a fan-out captions once, not twice.
    { id: "e3", source: DEP, target: DEP_B, kind: "uses" },
    { id: "e4", source: DEP, target: KUST, kind: "uses" },
    // A second kind out of the same card, to stack below the first.
    { id: "e5", source: DEP, target: CM, kind: "mounts" },
  ],
  stats: { counts: {}, durationMs: 1 },
};

vi.mock("../api/client", () => ({
  getLatest: vi.fn(async () => captionSnapshot),
  getNamespaces: vi.fn(async () => ["web"]),
  getContexts: vi.fn(async () => []),
  getManifest: vi.fn(async () => "kind: Deployment"),
  refresh: vi.fn(),
}));

const translateOf = (el: Element) => {
  const t = (el as HTMLElement).style.transform;
  // Skips the chip's own `translate(-50%, 0)` centring term: the pattern
  // requires px units.
  const m = /translate\((-?[\d.]+)px,\s*(-?[\d.]+)px\)/.exec(t);
  if (!m) throw new Error(`no translate in ${JSON.stringify(t)}`);
  return { x: +m[1], y: +m[2] };
};

const nodeEl = (id: string) =>
  document.querySelector(`.react-flow__node[data-id="${id}"]`);

/**
 * Open the namespace so its workloads are on screen. Expansion is the user's
 * now: a fresh view shows the cluster's namespaces and waits to be asked for
 * anything deeper, so a test that wants edges has to ask.
 */
async function renderExpanded(focus?: string) {
  if (focus) window.history.replaceState(null, "", `/?focus=${encodeURIComponent(focus)}`);
  renderApp();
  // ?focus= reveals its target, so the namespace is already open in that case —
  // clicking the toggle would close it again.
  if (!focus) {
    const toggle = await waitFor(() => {
      const el = nodeEl(NS)?.querySelector("[data-toggle]");
      if (!el) throw new Error("namespace not on screen yet");
      return el;
    });
    fireEvent.click(toggle);
  }
  await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());
}

// NOTE on why selection arrives through ?focus= rather than a click here.
// Selecting re-renders the canvas with a fresh node array; xyflow then drops its
// measurements and re-measures through ResizeObserver. A real observer reports
// again, but a stub only fires when a NEW element is observed — so under jsdom
// nothing re-measures and every edge silently disappears. Verified directly: 4
// edges with no selection, 0 after a click. Seeding the selection before the
// first paint measures once and keeps the edges, which is the state these tests
// are actually about. Which arrows get drawn is unit-tested in lib/tree.test.ts,
// where no DOM is involved at all.

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  window.history.replaceState(null, "", "/");
});

describe("edge captions", () => {
  // Scoped to the canvas: the details panel lists the same relationship names
  // in prose, and matching those would make every count wrong.
  const chipsFor = (text: string) => {
    const layer = document.querySelector(".react-flow__edgelabel-renderer");
    if (!layer) return [];
    return [...layer.querySelectorAll("*")].filter(
      (el) => el.children.length === 0 && el.textContent === text,
    );
  };

  // A caption chip must sit within its own source card's horizontal span —
  // that's what "clearly attached to this node" means measurably. Handle
  // geometry is degenerate under jsdom (getBoundingClientRect is all zeros), so
  // the assertion is span membership rather than exact bottom-centre alignment.
  const withinCard = (chipX: number, cardId: string) => {
    const left = translateOf(nodeEl(cardId)!).x;
    return chipX >= left && chipX <= left + NODE_W;
  };

  it("draws no relationship lines until a card is asked about", async () => {
    await renderExpanded();
    await new Promise((r) => setTimeout(r, 300));

    expect(chipsFor("uses")).toHaveLength(0);
    expect(chipsFor("mounts")).toHaveLength(0);
  });

  it("captions only the selected card's own wiring", async () => {
    await renderExpanded(DEP);
    console.log("DEBUG edges:", document.querySelectorAll(".react-flow__edge").length,
      "nodes:", document.querySelectorAll(".react-flow__node").length,
      "edgesSvg:", document.querySelector(".react-flow__edges")?.innerHTML.slice(0, 200),
      "panelHas:", document.body.textContent?.includes("uses"));

    const chips = await waitFor(() => {
      const found = chipsFor("uses");
      expect(found).toHaveLength(1); // fan-out: one caption per kind per source
      return found;
    });
    // Rendered in xyflow's HTML edge-label layer, which stacks above the whole
    // edge SVG — so no line can ever strike a caption through.
    expect(chips[0].closest(".react-flow__edgelabel-renderer")).toBeTruthy();
    expect(withinCard(translateOf(chips[0]).x, DEP)).toBe(true);
  });

  it("stacks a second kind below the first, under the same card", async () => {
    await renderExpanded(DEP);

    await waitFor(() => expect(chipsFor("uses")).toHaveLength(1));
    const uses = chipsFor("uses")[0];
    const mounts = chipsFor("mounts")[0];
    expect(withinCard(translateOf(mounts).x, DEP)).toBe(true);
    expect(translateOf(mounts).y).toBeCloseTo(translateOf(uses).y + CAPTION_STEP, 3);
  });

  it("shows a relationship pointing at the selected card, not only out of it", async () => {
    // worker is the target of DEP's "uses" and the source of a managed-by.
    await renderExpanded(DEP_B);

    // The incoming link is drawn, captioned under its own source card…
    await waitFor(() => expect(chipsFor("uses")).toHaveLength(1));
    // …while wiring that does not touch worker at all stays off the canvas.
    expect(chipsFor("mounts")).toHaveLength(0);
    expect(chipsFor("managed-by")).toHaveLength(0);
  });

  it("marks the managing controller on the card instead of drawing a line", async () => {
    await renderExpanded(DEP);
    await waitFor(() => expect(chipsFor("uses")).toHaveLength(1));

    // The relationship is still reported — as a mark on the card, which says the
    // same thing in a badge's worth of space instead of a line across the view.
    expect(chipsFor("managed-by")).toHaveLength(0);
    expect(within(nodeEl(DEP) as HTMLElement).getByText("Kustomization")).toBeInTheDocument();
  });
});
