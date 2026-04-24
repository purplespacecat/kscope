import { useEffect, useMemo, useState } from "react";
import { useNamespaces, useRefresh } from "../hooks/useGraph";
import type { Snapshot } from "../types/graph";

interface Props {
  snapshot: Snapshot | null | undefined;
}

// Left-side form. Lets the user pick which namespaces the next invocation
// should cover. Seeds itself from whatever scope produced the current snapshot.
export function ScopePanel({ snapshot }: Props) {
  const { data: available, isLoading: nsLoading, error: nsError } = useNamespaces();
  const refresh = useRefresh();

  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState("");

  // Re-hydrate selection from the server snapshot every time it changes —
  // this is what makes "refresh the browser and keep the same view" work.
  useEffect(() => {
    if (snapshot) setSelected(new Set(snapshot.scope.namespaces));
  }, [snapshot]);

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

  const onRun = () => {
    refresh.mutate({ namespaces: [...selected] });
  };

  return (
    <aside className="flex h-full w-72 flex-col border-r border-slate-200 bg-white">
      <div className="border-b border-slate-200 px-4 py-3">
        <div className="text-xs font-semibold uppercase tracking-wide text-slate-500">
          Scope
        </div>
        <div className="text-sm text-slate-500">Namespaces to discover</div>
      </div>

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
        <button
          type="button"
          onClick={onRun}
          disabled={selected.size === 0 || refresh.isPending}
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
    </aside>
  );
}
