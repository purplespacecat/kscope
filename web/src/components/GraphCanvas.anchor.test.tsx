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
// Every card is a top-level node now — the old `extent: "parent"` containers are
// gone with the packing they existed for — so node geometry is assertable here.
// What visibility MEANS is unit-tested in lib/tree.test.ts without a DOM; these
// tests cover only what needs one: toggles, anchoring, and selection semantics.
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

// Mirrors GraphCanvas's own constant; the box must clear the card, not merely
// sit a pixel below it.
const NODE_H = 56;

const NS = "core/namespace/web";
const DEP = "apps/deployment/web/api";
const POD_GROUP = `__kg__${DEP}__Pod`;
const pods = Array.from({ length: 6 }, (_, i) => `core/pod/web/api-6d4f${i}`);

// A SECOND namespace subtree is load-bearing, not scenery: it is what proves a
// selection leaves other branches alone, and what solo mode has to fold away.
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
// into a collapsed kind-group card once the Deployment is expanded.
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

const state = vi.hoisted(() => ({ snapshot: null as unknown as Snapshot }));

vi.mock("../api/client", () => ({
  getLatest: vi.fn(async () => state.snapshot),
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
  state.snapshot = snapshot;
});

/** The ▸/▾ badge inside a card — structure, as opposed to the card body. */
const toggleEl = (id: string) =>
  nodeEl(id)?.querySelector("[data-toggle]") as HTMLElement | null;

/** Open a path from the root down, one toggle at a time. */
async function expandPath(...ids: string[]) {
  for (const id of ids) {
    await waitFor(() => expect(toggleEl(id)).toBeTruthy());
    fireEvent.click(toggleEl(id)!);
  }
}

describe("expansion is the user's", () => {
  it("shows the cluster's namespaces and nothing deeper on first load", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(NS)).toBeTruthy());
    expect(nodeEl(NS2)).toBeTruthy();
    // One level only: a namespace's contents wait to be asked for.
    expect(nodeEl(DEP)).toBeNull();
  });

  it("keeps the toggled card at the same screen position while the tree reflows", async () => {
    renderApp();
    await expandPath(NS, DEP);
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());

    // The group card, not the Deployment: expanding a single-child chain moves
    // nothing, so anchoring it would assert nothing. Six members re-centre the
    // card over a much wider subtree, which is a reflow the eye would notice.
    const beforeScreen = screenPos(POD_GROUP);
    const beforeLayout = translateOf(nodeEl(POD_GROUP)!);
    fireEvent.click(nodeEl(POD_GROUP)!);

    // Both halves matter. The layout assertion proves the reflow actually
    // happened (otherwise the screen assertion is vacuous — it would hold with
    // anchoring deleted); the screen assertion proves the pan compensated.
    await waitFor(() => {
      expect(nodeEl(pods[0])).toBeTruthy();
      expect(translateOf(nodeEl(POD_GROUP)!)).not.toEqual(beforeLayout);
    });
    const afterScreen = screenPos(POD_GROUP);
    expect(afterScreen.x).toBeCloseTo(beforeScreen.x, 1);
    expect(afterScreen.y).toBeCloseTo(beforeScreen.y, 1);
  });

  // THE regression this whole change exists to prevent. Under the old model,
  // selecting re-derived the visible set from the selection, so clicking a
  // sibling silently threw away everything the user had opened.
  it("leaves the open tree exactly as it was when a card is selected", async () => {
    renderApp();
    await expandPath(NS, DEP);
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());
    const openBefore = document.querySelectorAll(".react-flow__node").length;

    fireEvent.click(nodeEl(DEP)!); // the card body: details, not structure
    await waitFor(() => expect(screen.queryByText(NOTHING_SELECTED)).toBeNull());

    expect(document.querySelectorAll(".react-flow__node")).toHaveLength(openBefore);
    expect(nodeEl(POD_GROUP)).toBeTruthy(); // still open
    expect(nodeEl(NS2)).toBeTruthy(); // sibling branch not pruned
  });

  it("expands and collapses from the card, which stays put throughout", async () => {
    renderApp();
    await expandPath(NS, DEP);
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());
    expect(nodeEl(pods[0])).toBeNull(); // collapsed: members not rendered

    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(pods[0])).toBeTruthy());

    // The card is the same node in both states — it never leaves the layout — so
    // the round trip is driven from it rather than from a replacement header.
    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(pods[0])).toBeNull());
    expect(nodeEl(POD_GROUP)).toBeTruthy();
  });

  it("ranks an expanded group's members below its card, not beside it", async () => {
    // Preserved intent from the old members-box test: expanding must grow the
    // tree downward. Sideways growth was the reported bug — a ~1300px box ranked
    // as the card's sibling shoved the rest off the viewport.
    renderApp();
    await expandPath(NS, DEP);
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());

    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(pods[0])).toBeTruthy());

    const card = translateOf(nodeEl(POD_GROUP)!);
    const member = translateOf(nodeEl(pods[0])!);
    expect(member.y).toBeGreaterThan(card.y); // below…
    expect(member.y - card.y).toBeGreaterThan(NODE_H); // …clear of the card itself
  });

  it("leaves the selection alone when a group card is toggled", async () => {
    renderApp();
    await expandPath(NS, DEP);
    await waitFor(() => expect(nodeEl(POD_GROUP)).toBeTruthy());
    expect(await screen.findByText(NOTHING_SELECTED)).toBeInTheDocument();

    fireEvent.click(nodeEl(POD_GROUP)!);
    await waitFor(() => expect(nodeEl(pods[0])).toBeTruthy());

    // Group cards aren't resources: expanding one must not hijack the details
    // panel.
    expect(screen.getByText(NOTHING_SELECTED)).toBeInTheDocument();
  });

  // A reveal can open several levels at once, so the target may land anywhere
  // in a large layout. Selection inside the canvas must NOT refit — that was the
  // old model's way of losing your place — but a pick from the sidebar has no
  // on-screen origin to preserve, so it re-frames, as it always did.
  it("re-frames when a node is revealed from the tree", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(NS)).toBeTruthy());
    const before = nodeEl(NS);

    // Open the sidebar tree down to the Deployment, then pick it there. The
    // sidebar keeps its own expansion state, so this says nothing about the
    // canvas until the click lands.
    const expandRow = (kind: string, name: string) => {
      const row = document.querySelector(`[title="${kind}: ${name}"]`)!;
      const caret = row.parentElement?.querySelector('[aria-label="Expand"]');
      if (caret) fireEvent.click(caret);
    };
    await waitFor(() => expect(document.querySelector('[title="Namespace: web"]')).toBeTruthy());
    expandRow("Namespace", "web");
    const row = await waitFor(() => document.querySelector('[title="Deployment: api"]')!);
    fireEvent.click(row);

    await waitFor(() => {
      expect(nodeEl(DEP)).toBeTruthy(); // revealed
      const after = nodeEl(NS);
      expect(after).toBeTruthy();
      expect(after).not.toBe(before); // remounted ⇒ refit
    });
  });
});

describe("solo mode", () => {
  const soloSwitch = () => screen.getByLabelText(/one branch at a time/i);

  it("is off until asked for, so two branches stay open side by side", async () => {
    renderApp();
    await expandPath(NS, NS2);
    await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());
    expect(nodeEl(DEP2)).toBeTruthy();
  });

  it("collapses the sibling branch when expanding with it on", async () => {
    renderApp();
    await expandPath(NS);
    await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());

    fireEvent.click(soloSwitch());
    await expandPath(NS2);

    await waitFor(() => expect(nodeEl(DEP2)).toBeTruthy());
    expect(nodeEl(DEP)).toBeNull(); // the other branch folded itself away
    expect(nodeEl(NS)).toBeTruthy(); // …but its card is still there to reopen
  });

  it("resurrects nothing when switched back off", async () => {
    renderApp();
    await expandPath(NS);
    fireEvent.click(soloSwitch());
    await expandPath(NS2);
    await waitFor(() => expect(nodeEl(DEP)).toBeNull());

    fireEvent.click(soloSwitch()); // off again
    // Nothing was hidden — things were genuinely collapsed — so turning the
    // mode off must not reopen them behind the user's back.
    await waitFor(() => expect(nodeEl(DEP2)).toBeTruthy());
    expect(nodeEl(DEP)).toBeNull();
  });
});

describe("climbing to the parent", () => {
  it("offers no climb at the top of the tree", async () => {
    renderApp();
    await waitFor(() => expect(nodeEl(NS)).toBeTruthy());
    expect(screen.queryByText(/show parent/i)).toBeNull();
  });

  it("raises the root and keeps the branch below it open", async () => {
    // Arrive as the k9s handoff does: ?focus= puts the resource at the top with
    // its ancestors off-screen.
    window.history.replaceState(null, "", `/?focus=${encodeURIComponent(DEP)}`);
    renderApp();
    await waitFor(() => expect(nodeEl(DEP)).toBeTruthy());
    expect(nodeEl("cluster")).toBeNull(); // ancestors not drawn
    expect(nodeEl(POD_GROUP)).toBeTruthy(); // own children are

    fireEvent.click(screen.getByText(/show parent/i));

    await waitFor(() => expect(nodeEl(NS)).toBeTruthy());
    // Context gained without losing the place: the branch is still open.
    expect(nodeEl(DEP)).toBeTruthy();
    expect(nodeEl(POD_GROUP)).toBeTruthy();
  });
});
