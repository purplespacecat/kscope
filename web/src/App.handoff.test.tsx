import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import * as api from "./api/client";
import type { Scope, Snapshot } from "./types/graph";

vi.mock("./components/GraphCanvas", () => ({
  GraphCanvas: () => <div data-testid="canvas" />,
}));

const node = (id: string, kind: string, name: string, parentId?: string) => ({
  id,
  kind,
  name,
  parentId,
  namespace: "app",
  health: "healthy" as const,
});

const dev: Snapshot = {
  scope: { context: "dev/ci1", namespaces: ["app"] },
  timestamp: "2026-09-20T09:00:00Z",
  cluster: { context: "dev/ci1", server: "https://x", version: "v1.33.0" },
  nodes: [node("cluster", "Cluster", "dev/ci1"), node("core/pod/app/api", "Pod", "api", "cluster")],
  edges: [],
  stats: { counts: {}, durationMs: 1 },
};

const prod: Snapshot = {
  ...dev,
  scope: { context: "prod/prod1", namespaces: ["payments"] },
  timestamp: "2026-09-20T10:00:00Z",
  cluster: { context: "prod/prod1", server: "https://y", version: "v1.33.0" },
  nodes: [node("cluster", "Cluster", "prod/prod1"), node("core/pod/payments/web", "Pod", "web", "cluster")],
};

vi.mock("./api/client", () => ({
  getLatest: vi.fn(),
  getNamespaces: vi.fn(async () => ["app"]),
  getContexts: vi.fn(async () => []),
  getManifest: vi.fn(async () => "kind: Pod"),
  refresh: vi.fn(),
  resolveFocus: vi.fn(),
}));

/** Stand in for the Wails runtime so a handoff can be driven from a test. */
function stubDesktopRuntime() {
  const handlers = new Map<string, (...a: unknown[]) => void>();
  (window as unknown as { runtime: unknown }).runtime = {
    EventsOn: (name: string, cb: (...a: unknown[]) => void) => {
      handlers.set(name, cb);
      return () => handlers.delete(name);
    },
  };
  return (payload: unknown) => handlers.get("kscope:focus")?.(payload);
}

const scopeFor = (ctx: string, ns: string): Scope => ({
  context: ctx,
  namespaces: [ns],
  includeInfra: false,
  includeCRDs: false,
});

const miss = (over: Record<string, unknown> = {}) => ({
  phase: "missing",
  name: "web",
  kind: "pods",
  namespace: "payments",
  context: "prod/prod1",
  scope: scopeFor("prod/prod1", "payments"),
  ...over,
});

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/");
  delete (window as unknown as { runtime?: unknown }).runtime;
  vi.mocked(api.getLatest).mockResolvedValue(dev);
  vi.mocked(api.refresh).mockResolvedValue(prod);
  vi.mocked(api.resolveFocus).mockResolvedValue("core/pod/payments/web");
});

describe("k9s handoff to a resource we do not have", () => {
  it("discovers the scope the handoff asked for", async () => {
    const emit = stubDesktopRuntime();
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());

    // The signal is passed too, so assert on the scope rather than the exact
    // argument list.
    await waitFor(() => expect(api.refresh).toHaveBeenCalled());
    expect(vi.mocked(api.refresh).mock.calls[0][0]).toEqual(scopeFor("prod/prod1", "payments"));
  });

  it("says which cluster it is discovering and what that replaces", async () => {
    const emit = stubDesktopRuntime();
    // Hold the pass open: a strip that appears and clears in one tick cannot be
    // observed, and is not what the user sees either.
    vi.mocked(api.refresh).mockReturnValue(new Promise<Snapshot>(() => {}));
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());

    const strip = await screen.findByRole("status");
    expect(strip.textContent).toContain("prod/prod1");
    expect(strip.textContent).toContain("payments");
    // The map it is about to throw away, named before it goes.
    expect(strip.textContent).toContain("dev/ci1");
  });

  it("lands on the resource once the pass finishes", async () => {
    const emit = stubDesktopRuntime();
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());

    await waitFor(() =>
      expect(api.resolveFocus).toHaveBeenCalledWith({
        name: "web",
        namespace: "payments",
        kind: "pods",
      }),
    );
    await waitFor(() =>
      expect(window.location.search).toContain(encodeURIComponent("core/pod/payments/web")),
    );
  });

  it("ignores a second handoff while a pass is already running", async () => {
    const emit = stubDesktopRuntime();
    let release!: (s: Snapshot) => void;
    vi.mocked(api.refresh).mockReturnValue(new Promise<Snapshot>((r) => (release = r)));
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());
    await waitFor(() => expect(api.refresh).toHaveBeenCalledTimes(1));
    emit(miss({ name: "other" }));

    expect(api.refresh).toHaveBeenCalledTimes(1);
    release(prod);
  });

  it("does not discover for a kind kscope never models", async () => {
    const emit = stubDesktopRuntime();
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss({ scope: undefined, reason: "unmapped", kind: "endpoints" }));

    const strip = await screen.findByRole("status");
    expect(strip.textContent).toMatch(/does not map|doesn't map/i);
    expect(api.refresh).not.toHaveBeenCalled();
  });

  it("explains a handoff that never said which namespace", async () => {
    const emit = stubDesktopRuntime();
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss({ scope: undefined, reason: "no-namespace" }));

    const strip = await screen.findByRole("status");
    expect(strip.textContent).toMatch(/namespace/i);
    expect(api.refresh).not.toHaveBeenCalled();
  });

  it("keeps the map when the pass fails, and says why", async () => {
    const emit = stubDesktopRuntime();
    vi.mocked(api.refresh).mockRejectedValue(new Error("list namespaces: forbidden"));
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());

    const strip = await screen.findByRole("status");
    await waitFor(() => expect(strip.textContent).toContain("forbidden"));
    // A failed pass never costs the user the map they had.
    expect(screen.getByTestId("canvas")).toBeInTheDocument();
  });

  it("can be cancelled while it runs", async () => {
    const emit = stubDesktopRuntime();
    vi.mocked(api.refresh).mockReturnValue(new Promise<Snapshot>(() => {}));
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());
    const cancel = await screen.findByRole("button", { name: /cancel/i });
    await userEvent.click(cancel);

    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });

  it("still just focuses when the resource is already here", async () => {
    const emit = stubDesktopRuntime();
    renderApp();
    await screen.findByTestId("canvas");

    emit({ phase: "resolved", id: "core/pod/app/api", name: "api", kind: "pods" });

    await waitFor(() =>
      expect(window.location.search).toContain(encodeURIComponent("core/pod/app/api")),
    );
    expect(api.refresh).not.toHaveBeenCalled();
  });
});

describe("getting back after a cross-cluster hop", () => {
  it("offers to return to the map it replaced", async () => {
    const emit = stubDesktopRuntime();
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss());
    await waitFor(() => expect(api.resolveFocus).toHaveBeenCalled());

    const back = await screen.findByRole("button", { name: /dev\/ci1/i });
    await userEvent.click(back);

    // The scope that produced the map it threw away — not a snapshot copy:
    // manifests are the large part and holding one would double memory for an
    // undo used once in a while.
    await waitFor(() => expect(api.refresh).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.refresh).mock.calls[1][0]).toEqual(dev.scope);
  });

  it("does not offer it when the hop stayed in the same cluster", async () => {
    const emit = stubDesktopRuntime();
    vi.mocked(api.refresh).mockResolvedValue({ ...prod, cluster: dev.cluster, scope: dev.scope });
    renderApp();
    await screen.findByTestId("canvas");

    emit(miss({ context: undefined, scope: scopeFor("dev/ci1", "other") }));
    await waitFor(() => expect(api.resolveFocus).toHaveBeenCalled());

    expect(screen.queryByRole("button", { name: /back to/i })).toBeNull();
  });
});
