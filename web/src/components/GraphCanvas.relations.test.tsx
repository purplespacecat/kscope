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

const NS = "core/namespace/web";
const DEP = "apps/deployment/web/api";
const SVC = "core/service/web/web";
const SA = "core/serviceaccount/web/api";
const CM = "core/configmap/web/cfg";
const KUST = "kustomize/kustomization/web/apps";

// Colours the outlines must use, from lib/display's EDGE_STYLE. The DOM
// normalises hex to rgb(), so compare in the form it will actually report.
const rgb = (hex: string) => {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};
const HEX = { references: "#3b82f6" };
const COLOR = {
  references: rgb("#3b82f6"),
  uses: rgb("#64748b"),
  selects: rgb("#10b981"),
  plain: rgb("#cbd5e1"),
};
const CONTAINMENT_STROKE = rgb("#cbd5e1");

const snapshot: Snapshot = {
  scope: { context: "kind-dev", namespaces: ["web"] },
  timestamp: "2026-08-11T09:00:00Z",
  cluster: { context: "kind-dev", server: "https://x", version: "v1.33.0" },
  nodes: [
    { id: "cluster", kind: "Cluster", name: "kind-dev", health: "healthy", synthetic: true },
    { id: NS, kind: "Namespace", name: "web", parentId: "cluster", health: "healthy" },
    { id: DEP, kind: "Deployment", name: "api", parentId: NS, namespace: "web", health: "healthy" },
    { id: SVC, kind: "Service", name: "web", parentId: NS, namespace: "web", health: "healthy" },
    { id: SA, kind: "ServiceAccount", name: "api", parentId: NS, namespace: "web", health: "healthy" },
    { id: CM, kind: "ConfigMap", name: "cfg", parentId: NS, namespace: "web", health: "healthy" },
    { id: KUST, kind: "Kustomization", name: "apps", parentId: NS, namespace: "web", health: "healthy" },
  ],
  edges: [
    // Three different things the Deployment takes part in, in both directions…
    { id: "e1", source: DEP, target: CM, kind: "references" },
    { id: "e2", source: DEP, target: SA, kind: "uses" },
    { id: "e3", source: SVC, target: DEP, kind: "selects" },
    // …and one that is a property of the card, not wiring between cards.
    { id: "e4", source: DEP, target: KUST, kind: "managed-by" },
  ],
  stats: { counts: {}, durationMs: 1 },
};

vi.mock("../api/client", () => ({
  getLatest: vi.fn(async () => snapshot),
  getNamespaces: vi.fn(async () => ["web"]),
  getContexts: vi.fn(async () => []),
  getManifest: vi.fn(async () => "kind: Deployment"),
  refresh: vi.fn(),
}));

const nodeEl = (id: string) =>
  document.querySelector(`.react-flow__node[data-id="${id}"]`) as HTMLElement | null;

const borderOf = (id: string) => nodeEl(id)?.style.border ?? "";

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>,
  );
}

/**
 * Selection arrives through ?focus= rather than a click. Selecting re-renders
 * the canvas with a fresh node array; xyflow drops its measurements and
 * re-measures through ResizeObserver, but a stub only fires for newly observed
 * elements — so under jsdom nothing re-measures and edges vanish. Seeding the
 * selection before the first paint measures once. Which relationships exist is
 * unit-tested in lib/tree.test.ts, with no DOM in the way.
 */
async function renderFocused(focus?: string) {
  if (focus) window.history.replaceState(null, "", `/?focus=${encodeURIComponent(focus)}`);
  renderApp();
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

beforeEach(() => {
  window.history.replaceState(null, "", "/");
});

describe("relationship outlines", () => {
  it("rings each related card in its relationship's colour", async () => {
    await renderFocused(DEP);

    await waitFor(() => expect(borderOf(CM)).toContain(COLOR.references));
    expect(borderOf(SA)).toContain(COLOR.uses);
    expect(borderOf(SVC)).toContain(COLOR.selects);
  });

  it("makes a related card obviously different from an unrelated one", async () => {
    // "Coloured" is not enough on its own — a 1px tint beside a 1px grey reads
    // as noise at a glance. The ring has to be thicker AND carry a halo.
    await renderFocused(DEP);
    await waitFor(() => expect(borderOf(CM)).toContain(COLOR.references));

    const width = (id: string) =>
      parseFloat(nodeEl(id)!.style.border.match(/^([\d.]+)px/)?.[1] ?? "0");
    expect(width(CM)).toBeGreaterThanOrEqual(3);
    expect(width(CM)).toBeGreaterThan(width(KUST));
    // A halo around the ring, in the same colour, so it carries across the
    // canvas rather than needing to be looked for. Alpha-suffixed hex is not
    // normalised to rgb() the way a plain border colour is.
    expect(nodeEl(CM)!.style.boxShadow).toContain(HEX.references);
    // And the card itself is tinted, so the whole shape reads as related.
    expect(nodeEl(CM)!.style.background).not.toBe("rgb(255, 255, 255)");
  });

  it("leaves unrelated cards alone", async () => {
    await renderFocused(DEP);
    await waitFor(() => expect(borderOf(CM)).toContain(COLOR.references));

    // managed-by is a mark on the card, not a relationship — so the
    // Kustomization is not ringed…
    expect(borderOf(KUST)).not.toContain(COLOR.references);
    expect(borderOf(KUST)).toContain(COLOR.plain);
    // …and the parent that merely contains everything is not either.
    expect(borderOf(NS)).toContain(COLOR.plain);
  });

  it("rings nothing until a card is selected", async () => {
    await renderFocused();
    for (const id of [CM, SA, SVC, KUST]) {
      expect(borderOf(id)).toContain(COLOR.plain);
    }
  });

  it("draws no relationship lines at all", async () => {
    await renderFocused(DEP);
    await waitFor(() => expect(borderOf(CM)).toContain(COLOR.references));

    // Containment is still drawn; nothing else is. Every path on the canvas is
    // a containment line, in containment grey.
    const strokes = [...document.querySelectorAll(".react-flow__edge-path")].map(
      (p) => (p as SVGPathElement).style.stroke,
    );
    expect(strokes.length).toBeGreaterThan(0);
    for (const stroke of strokes) expect(stroke).toBe(CONTAINMENT_STROKE);
  });
});

describe("relationship legend", () => {
  it("names each relationship in words, from the selection's side", async () => {
    await renderFocused(DEP);
    const legend = await waitFor(() => {
      const el = document.querySelector('[aria-label="Related to this"]');
      if (!el) throw new Error("no legend yet");
      return el as HTMLElement;
    });

    expect(legend.textContent).toContain("reads"); // → ConfigMap
    expect(legend.textContent).toContain("runs as"); // → ServiceAccount
    expect(legend.textContent).toContain("routed to by"); // ← Service
    // The API's own word for the last one, which said nothing to a reader.
    expect(legend.textContent).not.toContain("selects");
  });

  it("stays away when nothing is selected", async () => {
    await renderFocused();
    expect(document.querySelector('[aria-label="Related to this"]')).toBeNull();
  });

  it("marks the managing controller on the card rather than listing it", async () => {
    await renderFocused(DEP);
    const legend = await waitFor(() => {
      const el = document.querySelector('[aria-label="Related to this"]');
      if (!el) throw new Error("no legend yet");
      return el as HTMLElement;
    });

    expect(legend.textContent).not.toContain("managed by");
    expect(within(nodeEl(DEP)!).getByText("Kustomization")).toBeInTheDocument();
  });
});
