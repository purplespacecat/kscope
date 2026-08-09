import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TreePanel } from "./TreePanel";
import type { GraphNode } from "../types/graph";

// A minimal but shape-correct snapshot: cluster → namespace → deployment → pod,
// with the pod unhealthy so the rollup has something to surface.
function fixture(): GraphNode[] {
  return [
    { id: "cluster", kind: "Cluster", name: "test", health: "healthy" },
    {
      id: "core/namespace/web",
      kind: "Namespace",
      name: "web",
      parentId: "cluster",
      health: "healthy",
    },
    {
      id: "apps/deployment/web/api",
      kind: "Deployment",
      name: "api",
      namespace: "web",
      parentId: "core/namespace/web",
      health: "healthy",
    },
    {
      id: "core/pod/web/api-abc",
      kind: "Pod",
      name: "api-abc",
      namespace: "web",
      parentId: "apps/deployment/web/api",
      health: "error",
    },
  ];
}

describe("TreePanel", () => {
  it("starts with only the roots expanded", () => {
    render(<TreePanel nodes={fixture()} selectedId={null} onSelect={() => {}} />);
    // Root and its immediate children are visible…
    expect(screen.getByText("test")).toBeInTheDocument();
    expect(screen.getByText("web")).toBeInTheDocument();
    // …but the namespace is collapsed, so deeper rows aren't rendered.
    expect(screen.queryByText("api")).not.toBeInTheDocument();
  });

  it("expands a collapsed row via its chevron without selecting it", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<TreePanel nodes={fixture()} selectedId={null} onSelect={onSelect} />);

    // The namespace row's chevron is the Expand button that isn't the root's.
    const expanders = screen.getAllByRole("button", { name: "Expand" });
    await user.click(expanders[0]);

    expect(screen.getByText("api")).toBeInTheDocument();
    // stopPropagation: expanding must not also select the row
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("selects a node when its row is clicked", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<TreePanel nodes={fixture()} selectedId={null} onSelect={onSelect} />);

    await user.click(screen.getByText("web"));
    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({ id: "core/namespace/web" }),
    );
  });

  it("auto-expands the ancestry of an outside selection", () => {
    // Selecting in the graph (or via k9s handoff) must reveal the row here.
    render(
      <TreePanel
        nodes={fixture()}
        selectedId="core/pod/web/api-abc"
        onSelect={() => {}}
      />,
    );
    expect(screen.getByText("api-abc")).toBeInTheDocument();
  });

  it("filters by name, keeping the match's ancestry visible", async () => {
    const user = userEvent.setup();
    render(<TreePanel nodes={fixture()} selectedId={null} onSelect={() => {}} />);

    await user.type(screen.getByPlaceholderText("Find resource…"), "api-abc");

    // The match and its full path stay…
    expect(screen.getByText("api-abc")).toBeInTheDocument();
    expect(screen.getByText("web")).toBeInTheDocument();
    expect(screen.getByText("test")).toBeInTheDocument();
    // …and "api" the deployment is also kept — it's an ancestor of the match.
    expect(screen.getByText("api")).toBeInTheDocument();
  });

  it("shows 'No matches.' when the filter excludes everything", async () => {
    const user = userEvent.setup();
    render(<TreePanel nodes={fixture()} selectedId={null} onSelect={() => {}} />);

    await user.type(screen.getByPlaceholderText("Find resource…"), "zzz");
    expect(screen.getByText("No matches.")).toBeInTheDocument();
  });

  it("rolls the worst descendant health up to collapsed ancestors", () => {
    const { container } = render(
      <TreePanel nodes={fixture()} selectedId={null} onSelect={() => {}} />,
    );
    // The cluster row's health dot must be red: a crashing pod three levels
    // down surfaces at the collapsed root. bg-red-500 = HEALTH_DOT.error.
    const rows = container.querySelectorAll(".bg-red-500");
    expect(rows.length).toBeGreaterThan(0);
  });

  it("renders the empty state for an empty snapshot", () => {
    render(<TreePanel nodes={[]} selectedId={null} onSelect={() => {}} />);
    expect(screen.getByText("Snapshot is empty.")).toBeInTheDocument();
  });
});
