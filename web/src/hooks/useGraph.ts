import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../api/client";
import type { Scope, Snapshot } from "../types/graph";

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

export function useNamespaces() {
  return useQuery<string[]>({
    queryKey: ["namespaces"],
    queryFn: api.getNamespaces,
    staleTime: 60_000,
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
