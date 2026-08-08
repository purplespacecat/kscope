import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import type { KubeContext, Scope, Snapshot } from "../types/graph";

const LATEST_KEY = ["graph", "latest"] as const;

export function useLatest() {
  return useQuery<Snapshot | null>({
    queryKey: LATEST_KEY,
    queryFn: api.getLatest,
    // The server is the source of truth. Don't auto-refetch in the background;
    // the view only changes when the user invokes a new run.
    refetchOnWindowFocus: false,
    staleTime: Infinity,
  });
}

// Read from the local kubeconfig only — no cluster round-trip, so this stays
// fresh for the session.
export function useContexts() {
  return useQuery<KubeContext[]>({
    queryKey: ["contexts"],
    queryFn: api.getContexts,
    staleTime: Infinity,
  });
}

// Keyed by context so switching clusters refetches instead of showing the
// previous cluster's namespaces.
export function useNamespaces(kubeContext?: string) {
  return useQuery<string[]>({
    queryKey: ["namespaces", kubeContext ?? ""],
    queryFn: () => api.getNamespaces(kubeContext),
    staleTime: 60_000,
  });
}

// Manifest for one node. Keyed by snapshot timestamp so a new discovery
// naturally invalidates every cached manifest.
export function useManifest(id: string | null, snapshotTs: string | undefined) {
  return useQuery<string>({
    queryKey: ["manifest", snapshotTs, id],
    queryFn: () => api.getManifest(id!),
    enabled: !!id && !!snapshotTs,
    staleTime: Infinity,
    retry: false, // 404 = "no manifest", retrying won't change that
  });
}

export function useRefresh() {
  const qc = useQueryClient();
  return useMutation<Snapshot, Error, Scope>({
    mutationFn: api.refresh,
    onSuccess: (snap) => {
      // Write straight to the cache instead of invalidating + re-fetching:
      // we already have the new snapshot in-hand.
      qc.setQueryData(LATEST_KEY, snap);
    },
  });
}
