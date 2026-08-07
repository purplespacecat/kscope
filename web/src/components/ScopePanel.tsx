import { useEffect, useMemo, useRef, useState } from "react";
import { useContexts, useNamespaces, useRefresh } from "../hooks/useGraph";
import { DESKTOP_EVENTS, onDesktopEvent } from "../lib/desktop";
import type { Snapshot } from "../types/graph";

interface Props {
  snapshot: Snapshot | null | undefined;
}

// Left-side form. Lets the user pick which cluster and which namespaces the
// next invocation should cover. Seeds itself from whatever scope produced the
// current snapshot.
export function ScopePanel({ snapshot }: Props) {
  // "" means the kubeconfig's current-context, matching the server's reading
  // of an absent Scope.Context.
  const [kubeContext, setKubeContext] = useState("");
  const { data: contexts } = useContexts();
  const {
    data: available,
    isLoading: nsLoading,
    error: nsError,
  } = useNamespaces(kubeContext);
  const refresh = useRefresh();

  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [includeInfra, setIncludeInfra] = useState(true);
  const [includeCRDs, setIncludeCRDs] = useState(true);
  const [filter, setFilter] = useState("");

  // Re-hydrate selection from the server snapshot every time a new one lands
  // — this is what makes "refresh the browser and keep the same view" work.
  // Render-time adjustment instead of an effect (react-hooks lint): React
  // re-renders immediately with the new state, no flash of stale selection.
  const [seenSnapshot, setSeenSnapshot] = useState<Props["snapshot"]>(undefined);
  if (snapshot !== seenSnapshot) {
    setSeenSnapshot(snapshot);
    if (snapshot) {
      setKubeContext(snapshot.scope.context ?? "");
      setSelected(new Set(snapshot.scope.namespaces));
      setIncludeInfra(snapshot.scope.includeInfra ?? false);
      setIncludeCRDs(snapshot.scope.includeCRDs ?? false);
    }
  }

  // Namespace names are cluster-specific, so a kept selection would silently
  // scope the next run to namespaces that may not exist on the new cluster.
  const onContextChange = (next: string) => {
    setKubeContext(next);
    setSelected(new Set());
    setFilter("");
  };

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return (available ?? []).filter((n) => !q || n.toLowerCase().includes(q));
  }, [available, filter]);

  const toggle = (ns: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns);
      else next.add(ns);
      return next;
    });
  };

  const canRun = selected.size > 0 && !refresh.isPending;

  const onRun = () => {
    refresh.mutate({
      // Omitted when empty so the server keeps using current-context.
      ...(kubeContext ? { context: kubeContext } : {}),
      namespaces: [...selected],
      includeInfra,
      includeCRDs,
    });
  };

  // Desktop File → Run discovery (Ctrl-R) does exactly what the button does,
  // with the same guard so the shortcut can't fire an empty or concurrent run.
  // The handler lives in a ref so the subscription is created once instead of
  // being torn down and rebuilt on every keystroke in the filter box.
  const runRef = useRef(() => {});
  // No dep array: refresh the stored closure after every render so it always
  // sees current state. Writing it during render is what the refs lint rule
  // forbids.
  useEffect(() => {
    runRef.current = () => {
      if (canRun) onRun();
    };
  });
  useEffect(
    () => onDesktopEvent(DESKTOP_EVENTS.refresh, () => runRef.current()),
    [],
  );

  // Rendered as the top section of the left sidebar (App owns the column);
  // capped height so the resource tree below always keeps room.
  return (
    <section className="flex max-h-[45%] shrink-0 flex-col border-b border-slate-200">
      <div className="border-b border-slate-200 px-4 py-3">
        <div className="text-xs font-semibold uppercase tracking-wide text-slate-500">
          Scope
        </div>
        <div className="text-sm text-slate-500">Cluster and namespaces to discover</div>
      </div>

      {/* Only worth showing when there's a choice to make. */}
      {contexts && contexts.length > 1 && (
        <div className="border-b border-slate-200 px-3 pb-3 pt-2">
          <label
            htmlFor="kube-context"
            className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-slate-500"
          >
            Context
          </label>
          <select
            id="kube-context"
            value={kubeContext}
            onChange={(e) => onContextChange(e.target.value)}
            className="w-full rounded border border-slate-300 bg-white px-2 py-1 text-sm outline-none focus:border-slate-500"
          >
            <option value="">
              current-context
              {contexts.find((c) => c.current)
                ? ` (${contexts.find((c) => c.current)!.name})`
                : ""}
            </option>
            {contexts.map((c) => (
              <option key={c.name} value={c.name}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
      )}

      <div className="border-b border-slate-200 p-3">
        <input
          type="text"
          placeholder="Filter…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="w-full rounded border border-slate-300 px-2 py-1 text-sm outline-none focus:border-slate-500"
        />
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-2">
        {nsLoading && <p className="text-xs text-slate-400">Loading…</p>}
        {nsError && (
          <p className="text-xs text-red-600">Failed to load namespaces</p>
        )}
        {visible.map((ns) => (
          <label
            key={ns}
            className="flex cursor-pointer items-center gap-2 rounded px-1 py-1 text-sm hover:bg-slate-100"
          >
            <input
              type="checkbox"
              checked={selected.has(ns)}
              onChange={() => toggle(ns)}
            />
            <span className="truncate">{ns}</span>
          </label>
        ))}
      </div>

      <div className="border-t border-slate-200 p-3">
        <label className="mb-2 flex cursor-pointer items-center gap-2 text-sm text-slate-700">
          <input
            type="checkbox"
            checked={includeInfra}
            onChange={(e) => setIncludeInfra(e.target.checked)}
          />
          <span>
            Include infrastructure
            <span className="block text-[10px] text-slate-400">
              nodes + control plane
            </span>
          </span>
        </label>
        <label className="mb-2 flex cursor-pointer items-center gap-2 text-sm text-slate-700">
          <input
            type="checkbox"
            checked={includeCRDs}
            onChange={(e) => setIncludeCRDs(e.target.checked)}
          />
          <span>
            Include custom resources
            <span className="block text-[10px] text-slate-400">
              CRDs + their instances
            </span>
          </span>
        </label>
        <button
          type="button"
          onClick={onRun}
          disabled={!canRun}
          className="w-full rounded bg-slate-900 px-3 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:bg-slate-300"
        >
          {refresh.isPending ? "Running…" : `Run discovery (${selected.size})`}
        </button>
        {refresh.error && (
          <p className="mt-2 text-xs text-red-600">
            {(refresh.error as Error).message}
          </p>
        )}
      </div>
    </section>
  );
}
