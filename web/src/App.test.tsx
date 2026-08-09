import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Snapshot } from "./types/graph";

// GraphCanvas pulls in @xyflow/react, which needs ResizeObserver/DOMMatrix
// that jsdom lacks. These tests exercise App's layout, not the canvas.
vi.mock("./components/GraphCanvas", () => ({
  GraphCanvas: () => <div data-testid="canvas" />,
}));

const snapshot: Snapshot = {
  scope: { namespaces: ["web"] },
  timestamp: "2026-08-09T10:00:00Z",
  cluster: { context: "test", server: "https://x", version: "v1.33.0" },
  nodes: [
    { id: "cluster", kind: "Cluster", name: "test", health: "healthy" },
    {
      id: "core/namespace/web",
      kind: "Namespace",
      name: "web",
      parentId: "cluster",
      health: "healthy",
    },
  ],
  edges: [],
  stats: { counts: {}, durationMs: 1 },
};

vi.mock("./api/client", () => ({
  getLatest: vi.fn(async () => snapshot),
  getNamespaces: vi.fn(async () => ["web"]),
  getContexts: vi.fn(async () => []),
  getManifest: vi.fn(async () => "kind: Namespace"),
  refresh: vi.fn(),
}));

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  // App seeds selection from ?focus=; make each test start clean.
  window.history.replaceState(null, "", "/");
});

describe("sidebar collapsing", () => {
  it("collapses via the header button and reopens via the floating one", async () => {
    const user = userEvent.setup();
    renderApp();

    // Sidebar content is there (scope panel heading); no reopen button yet.
    expect(await screen.findByText("Scope")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Expand sidebar" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(screen.queryByText("Scope")).not.toBeInTheDocument();

    // The floating reopen button appears where the collapse button was.
    await user.click(screen.getByRole("button", { name: "Expand sidebar" }));
    expect(screen.getByText("Scope")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Expand sidebar" }),
    ).not.toBeInTheDocument();
  });

  it("shows a cluster overview when nothing is selected", async () => {
    renderApp();
    // The panel is persistent: no selection → overview, not absence.
    expect(await screen.findByText("Overview")).toBeInTheDocument();
    expect(
      screen.getByText("Select a resource in the tree or graph to inspect it."),
    ).toBeInTheDocument();
  });

  it("collapsing details keeps the selection; reopening needs no re-select", async () => {
    const user = userEvent.setup();
    renderApp();

    // Select the namespace in the tree → details replace the overview.
    // Queried by the row's title attribute: ScopePanel's namespace checkbox
    // also renders the text "web", so plain text would be ambiguous.
    await user.click(await screen.findByTitle("Namespace: web"));
    expect(await screen.findByText("Health")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Collapse details" }));
    expect(screen.queryByText("Health")).not.toBeInTheDocument();

    // Selection survived: the floating button restores the same node.
    await user.click(screen.getByRole("button", { name: "Expand details" }));
    expect(screen.getByText("Health")).toBeInTheDocument();
  });
});
