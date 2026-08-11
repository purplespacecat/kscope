import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
// fireEvent, not user-event: a full pointer sequence reaches d3-zoom's mousedown
// handler, which dereferences `event.view` — null on jsdom-dispatched events.
// Only the React onClick matters here.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "../App";
import type { Snapshot } from "../types/graph";

// @xyflow/react needs browser APIs jsdom lacks. This is the minimum to get the
// canvas rendering headlessly, which is why App.test.tsx mocks it out instead.
//
// KNOWN LIMIT: nodes inside a group container (`extent: "parent"`) come out with
// garbage positions here — xyflow clamps them against a parent it measures as
// 0x0, so all members land on one point. Container geometry therefore can't be
// asserted in jsdom; the group-card anchoring maths is covered by unit tests in
// lib/viewport.test.ts instead, and the tests below stick to behaviour that does
// survive: top-level node geometry, and toggle/selection semantics.
class ResizeObserverStub {
  private cb: ResizeObserverCallback;
  constructor(cb: ResizeObserverCallback) {
    this.cb = cb;
  }
  observe(target: Element) {
    this.cb([{ target } as ResizeObserverEntry], this as unknown as ResizeObserver);
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

const NS = "core/namespace/web";
const DEP = "apps/deployment/web/api";
const POD_GROUP = `__kg__${DEP}__Pod`;
const pods = Array.from({ length: 6 }, (_, i) => `core/pod/web/api-6d4f${i}`);

// A SECOND namespace subtree is load-bearing, not scenery. With only one branch,
// focusSubgraph returns a byte-identical node and edge set before and after DEP
// is selected — the spine re-adds cluster and ns, then descends to the same
// pods — so the layout never changes, the anchor delta is (0,0), and an
// anchoring assertion would pass even with the feature deleted. Selecting DEP
// prunes this sibling, which is what makes DEP actually move.
const NS2 = "core/namespace/other";
const DEP2 = "apps/deployment/other/worker";
const pods2 = Array.from({ length: 6 }, (_, i) => `core/pod/other/worker-8a2b${i}`);

const pod = (id: string, parentId: string, namespace: string) => ({
  id,
  kind: "Pod",
  name: id.split("/").pop()!,
  parentId,
  namespace,
  health: "healthy" as const,
});

// Each deployment holds 6 same-kind leaves — past GROUP_AT — so its pods fold
// into a collapsed kind-group card. 17 nodes total stays under NODE_BUDGET (40),
// so the unfocused view really does show both branches.
const snapshot: Snapshot = {
  scope: { context: "kind-dev", namespaces: ["web", "other"] },
  timestamp: "2026-08-11T09:00:00Z",
  cluster: {
    context: "kind-dev",
    server: "https://127.0.0.1:6443",
    version: "v1.33.0",
  },
  nodes: [
    { id: "cluster", kind: "Cluster", name: "kind-dev", health: "healthy", synthetic: true },
    { id: NS, kind: "Namespace", name: "web", parentId: "cluster", health: "healthy" },
    { id: DEP, kind: "Deployment", name: "api", parentId: NS, namespace: "web", health: "healthy" },
    ...pods.map((id) => pod(id, DEP, "web")),
    { id: NS2, kind: "Namespace", name: "other", parentId: "cluster", health: "healthy" },
    { id: DEP2, kind: "Deployment", name: "worker", parentId: NS2, namespace: "other", health: "healthy" },
    ...pods2.map((id) => pod(id, DEP2, "other")),
  ],
  edges: [],
  stats: { counts: { Pod: 12 }, durationMs: 1 },
};

vi.mock("../api/client", () => ({
  getLatest: vi.fn(async () => snapshot),
  getNamespaces: vi.fn(async () => ["web"]),
  getContexts: vi.fn(async () => []),
  getManifest: vi.fn(async () => "kind: Pod"),
  refresh: vi.fn(),
}));

const translateOf = (el: Element) => {
  const t = (el as HTMLElement).style.transform;
  const m = /translate\((-?[\d.]+)px,\s*(-?[\d.]+)px\)/.exec(t);
  if (!m) throw new Error(`no translate in ${JSON.stringify(t)}`);
  return { x: +m[1], y: +m[2] };
};

const zoomOf = (el: Element) => {
  const m = /scale\((-?[\d.]+)\)/.exec((el as HTMLElement).style.transform);
  return m ? +m[1] : 1;
};

const nodeEl = (id: string) =>
  document.querySelector(`.react-flow__node[data-id="${id}"]`);

/**
 * Where a node actually sits on screen: the viewport transform applied to the
 * node's own. This is what the eye tracks, and what must not move.
 */
function screenPos(id: string) {
  const vp = document.querySelector(".react-flow__viewport");
  if (!vp) throw new Error("no viewport");
  const node = nodeEl(id);
  if (!node) throw new Error(`node ${id} not rendered`);
  const v = translateOf(vp);
  const p = translateOf(node);
  const z = zoomOf(vp);
  return { x: v.x + p.x * z, y: v.y + p.y * z };
}

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>,
  );
}

const NOTHING_SELECTED = "Select a resource in the tree or graph to inspect it.";

beforeEach(() => {
  window.history.replaceState(null, "", "/");
});

describe("focus anchoring", () => {
  it("keeps a clicked resource at the same screen position while its subgraph is rebuilt", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());

    const beforeScreen = screenPos(DEP);
    const beforeLayout = translateOf(nodeEl(DEP)!);
    fireEvent.click(nodeEl(DEP)!);

    // Both halves matter. The layout assertion proves the reflow actually
    // happened (otherwise the screen-position assertion is vacuous — it would
    // hold even with anchoring removed); the screen assertion proves the pan
    // compensated for it.
    await waitFor(() => {
      expect(translateOf(nodeEl(DEP)!)).not.toEqual(beforeLayout);
      const afterScreen = screenPos(DEP);
      expect(afterScreen.x).toBeCloseTo(beforeScreen.x, 1);
      expect(afterScreen.y).toBeCloseTo(beforeScreen.y, 1);
    });
  });

  it("expands a group card and collapses it again", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());
    expect(nodeEl(pods[0])).toBeNull(); // collapsed: members not rendered

    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(`${POD_GROUP}__h`)).toBeTruthy());
    expect(nodeEl(pods[0])).toBeTruthy();

    // Round-trip guards the toggle direction: it is read from the clicked card's
    // id, so a card that has just expanded must collapse rather than re-expand.
    fireEvent.click(nodeEl(`${POD_GROUP}__h`)!);
    await waitFor(() => expect(nodeEl(`${POD_GROUP}__h`)).toBeNull());
    expect(nodeEl(pods[0])).toBeNull();
  });

  // The remount decision needs its own observable. fitView is a no-op under
  // jsdom (nothing is measured), so "did the view re-frame?" can't distinguish
  // the two paths — but DOM element identity can: changing the key unmounts the
  // subtree, so every node element is rebuilt.
  it("does not remount the canvas for a selection made in the canvas", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());

    const before = nodeEl(DEP);
    fireEvent.click(nodeEl(DEP)!);
    await waitFor(() => expect(nodeEl(NS2)).toBeNull()); // subgraph did rebuild

    expect(nodeEl(DEP)).toBe(before); // same element ⇒ no remount ⇒ no refit
  });

  it("remounts the canvas for a selection made in the tree", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());

    const before = nodeEl(DEP);
    const row = await waitFor(() => document.querySelector('[title="Namespace: web"]')!);
    fireEvent.click(row);

    // A tree click has no on-screen origin to preserve, so this path keeps the
    // fit-the-new-subgraph behaviour, which is driven by the remount.
    await waitFor(() => expect(nodeEl(DEP)).not.toBe(before));
  });

  it("collapses when the expanded group's container background is clicked", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());

    fireEvent.click(nodeEl(POD_GROUP)!); // the collapsed card → expand
    await waitFor(() => expect(nodeEl(`${POD_GROUP}__h`)).toBeTruthy());

    // Once expanded, the node carrying id POD_GROUP is the translucent container
    // box, not the card — same id, opposite state. Its padding and the gaps
    // between members are clickable, and clicking there must collapse the group.
    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(`${POD_GROUP}__h`)).toBeNull());
    expect(nodeEl(pods[0])).toBeNull();
  });

  it("leaves the selection alone when a group card is toggled", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());
    expect(await screen.findByText(NOTHING_SELECTED)).toBeInTheDocument();

    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(`${POD_GROUP}__h`)).toBeTruthy());

    // Group cards aren't resources: expanding one must not hijack the details
    // panel or re-root the focus subgraph.
    expect(screen.getByText(NOTHING_SELECTED)).toBeInTheDocument();
  });
});
