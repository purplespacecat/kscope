import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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
class ResizeObserverStub {
  private cb: ResizeObserverCallback;
  constructor(cb: ResizeObserverCallback) {
    this.cb = cb;
  }
  observe(target: Element) {
    setTimeout(
      () => this.cb([{ target } as ResizeObserverEntry], this as unknown as ResizeObserver),
      0,
    );
  }
  unobserve() {}
  disconnect() {}
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
  ],
  edges: [
    { id: "e1", source: DEP, target: KUST, kind: "managed-by" },
    { id: "e2", source: DEP_B, target: KUST, kind: "managed-by" },
    { id: "e3", source: DEP, target: DEP_B, kind: "uses" },
    { id: "e4", source: DEP, target: KUST, kind: "uses" },
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
  // A caption chip must sit within its own source card's horizontal span —
  // that's what "clearly attached to this node" means measurably. Handle
  // measurement is degenerate under jsdom (getBoundingClientRect is all
  // zeros), so the assertion is span membership rather than exact
  // bottom-centre alignment.
  const withinCard = (chipX: number, cardId: string) => {
    const left = translateOf(nodeEl(cardId)!).x;
    return chipX >= left && chipX <= left + NODE_W;
  };

  it("pins each caption to its own source card, above the edge lines", async () => {
    renderApp();
    const chips = await waitFor(() => {
      const found = screen.getAllByText("managed-by");
      expect(found).toHaveLength(2); // fan-in: one per source, none at midpoints
      return found;
    });

    // "Never crossed by lines" is structural: the chips render in xyflow's
    // HTML edge-label layer, which stacks above the whole edge SVG.
    for (const chip of chips) {
      expect(chip.closest(".react-flow__edgelabel-renderer")).toBeTruthy();
    }

    // Each chip hangs under its own managed deployment — not the shared
    // target, not the other source.
    const xs = chips.map((c) => translateOf(c).x).sort((a, b) => a - b);
    const cards = [DEP, DEP_B].sort(
      (a, b) => translateOf(nodeEl(a)!).x - translateOf(nodeEl(b)!).x,
    );
    expect(withinCard(xs[0], cards[0])).toBe(true);
    expect(withinCard(xs[1], cards[1])).toBe(true);
    expect(withinCard(xs[0], cards[1])).toBe(false);
    expect(withinCard(xs[1], cards[0])).toBe(false);
  });

  it("captions a kind once per source and stacks different kinds below it", async () => {
    renderApp();
    await waitFor(() => expect(screen.getAllByText("managed-by")).toHaveLength(2));

    // Two "uses" edges leave DEP; the caption appears once.
    const uses = screen.getAllByText("uses");
    expect(uses).toHaveLength(1);

    // …under DEP, one step below DEP's "managed-by" chip instead of on top
    // of it.
    const mine = screen
      .getAllByText("managed-by")
      .find((c) => withinCard(translateOf(c).x, DEP))!;
    expect(withinCard(translateOf(uses[0]).x, DEP)).toBe(true);
    expect(translateOf(uses[0]).y).toBeCloseTo(translateOf(mine).y + CAPTION_STEP, 3);
  });
});
