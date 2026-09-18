import { describe, expect, it } from "vitest";
import {
  gitopsManagerId,
  health,
  edgeMeaning,
  edgePhrase,
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

describe("edgePhrase", () => {
  it("says what a Service actually does to a Pod", () => {
    expect(edgePhrase("selects", "out", "Service")).toBe("routes to");
    expect(edgePhrase("selects", "in", "Service")).toBe("routed to by");
  });

  // Same edge kind, different source, genuinely different sentence: a policy
  // governs a Pod, it does not route anything to it.
  it("says something different when the source is a NetworkPolicy", () => {
    expect(edgePhrase("selects", "out", "NetworkPolicy")).toBe("applies to");
    expect(edgePhrase("selects", "in", "NetworkPolicy")).toBe("governed by");
  });

  it("reads as a sentence for the kinds that do not vary", () => {
    expect(edgePhrase("references", "out")).toBe("reads");
    expect(edgePhrase("references", "in")).toBe("read by");
    expect(edgePhrase("uses", "out")).toBe("runs as");
    expect(edgePhrase("scheduled-on", "out")).toBe("runs on");
    expect(edgePhrase("binds", "out")).toBe("bound to");
  });

  it("falls back to the raw kind for an edge it has no words for", () => {
    expect(edgePhrase("frobnicates", "out")).toBe("frobnicates");
    expect(edgePhrase("frobnicates", "in")).toBe("frobnicates ←");
  });
});

describe("edgeMeaning", () => {
  it("explains how the relationship was actually inferred", () => {
    // The point is to say something the phrase itself does not.
    expect(edgeMeaning("selects", "Service")).toMatch(/label/i);
    expect(edgeMeaning("references", undefined)).toMatch(/env/i);
    expect(edgeMeaning("mounts", undefined)).toMatch(/volume/i);
  });

  it("distinguishes the two things a label-selector match can mean", () => {
    const svc = edgeMeaning("selects", "Service");
    const np = edgeMeaning("selects", "NetworkPolicy");
    expect(svc).not.toBe(np);
    expect(np).toMatch(/polic/i);
  });

  it("says nothing rather than something empty for a kind it has no words for", () => {
    expect(edgeMeaning("frobnicates", undefined)).toBeUndefined();
  });
});
