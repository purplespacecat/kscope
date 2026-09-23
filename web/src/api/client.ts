import type { KubeContext, Scope, Snapshot } from "../types/graph";

// `null` means "server has no snapshot yet" (HTTP 204), distinct from an
// error. Callers render the empty state in that case.
export async function getLatest(): Promise<Snapshot | null> {
  const res = await fetch("/api/graph/latest");
  if (res.status === 204) return null;
  if (!res.ok) throw new Error(`GET /api/graph/latest: ${res.status}`);
  return (await res.json()) as Snapshot;
}

export async function getContexts(): Promise<KubeContext[]> {
  const res = await fetch("/api/contexts");
  if (!res.ok) throw new Error(`GET /api/contexts: ${res.status}`);
  const body = (await res.json()) as { contexts: KubeContext[] };
  return body.contexts;
}

// An empty kubeContext means "the kubeconfig's current-context", so the param
// is only sent when the user has actually chosen something else.
export async function getNamespaces(kubeContext?: string): Promise<string[]> {
  const qs = kubeContext ? `?context=${encodeURIComponent(kubeContext)}` : "";
  const res = await fetch(`/api/namespaces${qs}`);
  if (!res.ok) throw new Error(`GET /api/namespaces: ${res.status}`);
  const body = (await res.json()) as { namespaces: string[] };
  return body.namespaces;
}

// Node IDs contain slashes; they're appended as-is (the server route uses a
// trailing wildcard). Returns raw redacted YAML.
export async function getManifest(id: string): Promise<string> {
  const res = await fetch(`/api/node/manifest/${id}`);
  if (res.status === 404) throw new Error("No manifest in this snapshot");
  if (!res.ok) throw new Error(`GET manifest: ${res.status}`);
  return res.text();
}

export async function refresh(scope: Scope, signal?: AbortSignal): Promise<Snapshot> {
  const res = await fetch("/api/graph/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(scope),
    // Aborting is safe: the server stores a snapshot only on success, so a
    // cancelled pass never costs the caller the map they already had.
    signal,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`POST /api/graph/refresh: ${res.status} ${text}`);
  }
  return (await res.json()) as Snapshot;
}

/** Reference the k9s handoff gives us, before we know the node's id. */
export interface FocusRef {
  name: string;
  namespace?: string;
  kind?: string;
}

/**
 * Turn a name (plus optional namespace and kind) into a node id in the current
 * snapshot. Server-side so the tie-breaking rules live in one place — a
 * TypeScript copy of graph.ResolveNode would drift from it.
 */
export async function resolveFocus(ref: FocusRef): Promise<string | null> {
  const q = new URLSearchParams({ name: ref.name });
  if (ref.namespace) q.set("namespace", ref.namespace);
  if (ref.kind) q.set("kind", ref.kind);
  const res = await fetch(`/api/focus/resolve?${q}`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`GET /api/focus/resolve: ${res.status}`);
  return ((await res.json()) as { id: string }).id;
}
