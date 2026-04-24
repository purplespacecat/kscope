import type { Scope, Snapshot } from "../types/graph";

// `null` means "server has no snapshot yet" (HTTP 204), distinct from an
// error. Callers render the empty state in that case.
export async function getLatest(): Promise<Snapshot | null> {
  const res = await fetch("/api/graph/latest");
  if (res.status === 204) return null;
  if (!res.ok) throw new Error(`GET /api/graph/latest: ${res.status}`);
  return (await res.json()) as Snapshot;
}

export async function getNamespaces(): Promise<string[]> {
  const res = await fetch("/api/namespaces");
  if (!res.ok) throw new Error(`GET /api/namespaces: ${res.status}`);
  const body = (await res.json()) as { namespaces: string[] };
  return body.namespaces;
}

export async function refresh(scope: Scope): Promise<Snapshot> {
  const res = await fetch("/api/graph/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(scope),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`POST /api/graph/refresh: ${res.status} ${text}`);
  }
  return (await res.json()) as Snapshot;
}
