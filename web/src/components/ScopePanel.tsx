import { useEffect, useMemo, useRef, useState } from "react";
import { useContexts, useNamespaces, useRefresh } from "../hooks/useGraph";
import { DESKTOP_EVENTS, onDesktopEvent } from "../lib/desktop";
import type { Snapshot } from "../types/graph";
import { NamespacePicker } from "./NamespacePicker";

// How many selected names the sidebar summary spells out before collapsing the
// rest into "+N more" — enough to recognise the scope, few enough that a
// select-all can't make the section tall.
const SUMMARY_NAMES = 3;

interface Props {
  snapshot: Snapshot | null | undefined;
  /** Collapse the whole sidebar (button lives in this panel's header). */
  onCollapse: () => void;
}

// Left-side form. Lets the user pick which cluster and which namespaces the
// next invocation should cover. Seeds itself from whatever scope produced the
// current snapshot.
export function ScopePanel({ snapshot, onCollapse }: Props) {
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
  // The namespace list and its filter live in a modal — the sidebar is too
  // narrow to show a real cluster's namespaces without scrolling a short window.
  const [pickerOpen, setPickerOpen] = useState(false);

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
  };

  // Keeps the chosen scope readable without opening the modal — the inline list
  // used to show it, so this replaces information rather than adding it.
  const summary = useMemo(() => {
    const names = [...selected].sort();
    if (names.length === 0) return "None selected";
    const shown = names.slice(0, SUMMARY_NAMES).join(", ");
    const rest = names.length - SUMMARY_NAMES;
    return rest > 0 ? `${shown} +${rest} more` : shown;
  }, [selected]);

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
  // being torn down and rebuilt whenever the scope changes.
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

  // Rendered as the top section of the left sidebar (App owns the column). No
  // height cap needed now the namespace list is in a modal: the section is short
  // and fixed, so the resource tree below gets the rest of the column.
  return (
    <section className="flex shrink-0 flex-col border-b border-slate-200">
      <div className="flex items-start justify-between border-b border-slate-200 px-4 py-3">
        <div>
          <div className="text-xs font-semibold uppercase tracking-wide text-slate-500">
            Scope
          </div>
          <div className="text-sm text-slate-500">Cluster and namespaces to discover</div>
        </div>
        <button
          type="button"
          onClick={onCollapse}
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
          className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-slate-400 hover:bg-slate-100 hover:text-slate-700"
        >
          <svg viewBox="0 0 8 8" className="h-2.5 w-2.5 fill-current">
            <path d="M6 0 L2 4 L6 8 Z" />
          </svg>
        </button>
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
        <button
          type="button"
          onClick={() => setPickerOpen(true)}
          disabled={nsLoading || !available?.length}
          className="flex w-full items-center justify-between gap-2 rounded border border-slate-300 bg-white px-2 py-1.5 text-sm text-slate-700 hover:bg-slate-50 disabled:cursor-not-allowed disabled:bg-slate-50 disabled:text-slate-400"
        >
          <span>Namespaces</span>
          <span className="shrink-0 text-xs text-slate-500">
            {nsLoading
              ? "loading…"
              : // "of 0" would be a lie while the list is unavailable.
                available
                ? `${selected.size} of ${available.length}`
                : `${selected.size} selected`}
          </span>
        </button>
        {/* Kept visible even when the fetch failed: the summary is the only place
            the committed scope is shown, and hiding it while the trigger is
            disabled would leave no way to see what the next run would cover.
            Truncated rather than wrapped so the section's height stays fixed. */}
        <p className="mt-1 truncate text-[10px] text-slate-400" title={summary}>
          {summary}
        </p>
        {nsError && (
          <p className="mt-1 text-xs text-red-600">Failed to load namespaces</p>
        )}
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

      {/* Mounting is opening: the picker seeds its draft from `selected` on
          mount, so there's no open/close state to keep in sync. */}
      {pickerOpen && (
        <NamespacePicker
          available={available ?? []}
          selected={selected}
          onDone={(next) => {
            setSelected(next);
            setPickerOpen(false);
          }}
          onCancel={() => setPickerOpen(false)}
        />
      )}
    </section>
  );
}
