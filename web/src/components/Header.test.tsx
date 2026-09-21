import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Header } from "./Header";
import type { Snapshot } from "../types/graph";

const snapshot = (errors?: string[]): Snapshot => ({
  scope: { context: "dev/ci1", namespaces: ["app"] },
  timestamp: "2026-09-20T09:00:00Z",
  cluster: { context: "dev/ci1", server: "https://x", version: "v1.33.0" },
  nodes: [],
  edges: [],
  stats: { counts: {}, durationMs: 1, ...(errors ? { errors } : {}) },
});

describe("discovery errors", () => {
  // A pass whose lists were refused still writes a snapshot and still replaces
  // the previous map. Saying nothing turns a permissions problem into a
  // mysteriously empty graph.
  it("says how many lists a pass skipped", () => {
    render(<Header snapshot={snapshot(["pods: forbidden", "secrets: forbidden"])} />);
    expect(screen.getByText(/2 skipped/i)).toBeInTheDocument();
  });

  it("names the failures it is summarising, on hover", () => {
    render(<Header snapshot={snapshot(["pods: forbidden", "secrets: forbidden"])} />);
    expect(screen.getByText(/2 skipped/i).title).toContain("pods: forbidden");
  });

  // 1,160 near-identical rate-limiter timeouts is the real shape of this field
  // on a Crossplane cluster; listing them all would be useless.
  it("collapses repeats rather than listing every one", () => {
    const many = Array.from({ length: 40 }, () => "apis.hub.traefik.io: context deadline exceeded");
    render(<Header snapshot={snapshot([...many, "pods: forbidden"])} />);
    const el = screen.getByText(/41 skipped/i);
    expect(el.title).toContain("(40×)");
    expect(el.title).toContain("pods: forbidden");
  });

  it("stays out of the way when the pass was clean", () => {
    render(<Header snapshot={snapshot()} />);
    expect(screen.queryByText(/skipped/i)).toBeNull();
  });
});
