import { describe, expect, it } from "vitest";
import {
  gitopsManagerId,
  health,
  incomingEdgeLabel,
  kindDocsUrl,
  worseOf,
} from "./display";
import type { GraphNode } from "../types/graph";

describe("worseOf", () => {
  it("ranks error > warning > unknown > healthy", () => {
    expect(worseOf("healthy", "error")).toBe("error");
    expect(worseOf("error", "healthy")).toBe("error");
    expect(worseOf("healthy", "warning")).toBe("warning");
    // unknown outranks healthy: "we can't tell" must not roll up as green
    expect(worseOf("healthy", "unknown")).toBe("unknown");
    expect(worseOf("unknown", "warning")).toBe("warning");
  });

  it("is idempotent on equal inputs", () => {
    expect(worseOf("warning", "warning")).toBe("warning");
  });
});

describe("health", () => {
  it("defaults to unknown for pre-M1 snapshots without a health field", () => {
    expect(health({ id: "x", kind: "Pod", name: "p" } as GraphNode)).toBe("unknown");
    expect(
      health({ id: "x", kind: "Pod", name: "p", health: "healthy" } as GraphNode),
    ).toBe("healthy");
  });
});

describe("incomingEdgeLabel", () => {
  it("inverts known edge kinds", () => {
    expect(incomingEdgeLabel("mounts")).toBe("mounted by");
    expect(incomingEdgeLabel("scheduled-on")).toBe("hosts");
    expect(incomingEdgeLabel("managed-by")).toBe("manages");
  });

  it("falls back to an arrowed form for unknown kinds", () => {
    expect(incomingEdgeLabel("frobnicates")).toBe("frobnicates ←");
  });
});

describe("gitopsManagerId", () => {
  // This reconstructs the backend's node-ID format
  // (<group>/<kind-lowercased>/<ns>/<name>, internal/graph/discover.go
  // nodeID()) — if these fail, the GitOps card links to nowhere.
  it("builds a Kustomization id in the backend's format", () => {
    expect(
      gitopsManagerId({ tool: "flux", kind: "Kustomization", name: "apps", namespace: "flux-system" }),
    ).toBe("kustomize.toolkit.fluxcd.io/kustomization/flux-system/apps");
  });

  it("builds a HelmRelease id in the backend's format", () => {
    expect(
      gitopsManagerId({ tool: "flux", kind: "HelmRelease", name: "tempo", namespace: "monitoring" }),
    ).toBe("helm.toolkit.fluxcd.io/helmrelease/monitoring/tempo");
  });
});

describe("kindDocsUrl", () => {
  it("links known kinds and labels by doc site", () => {
    expect(kindDocsUrl("Deployment")?.label).toBe("Kubernetes docs");
    expect(kindDocsUrl("Kustomization")?.label).toBe("Flux docs");
  });

  it("returns null for unknown kinds instead of guessing", () => {
    expect(kindDocsUrl("SomeRandomCRD")).toBeNull();
  });

  it("resolves control-plane components by name", () => {
    expect(kindDocsUrl("Component", "api-server")?.url).toContain("kube-apiserver");
    expect(kindDocsUrl("Component", "datastore (kine)")?.url).toContain("k3s.io");
    expect(kindDocsUrl("Component", "mystery")).toBeNull();
  });
});
