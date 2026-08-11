import { useEffect, useMemo, useRef, useState } from "react";

interface Props {
  available: string[];
  /** Committed selection; copied into a draft so Cancel can discard. */
  selected: Set<string>;
  onDone: (next: Set<string>) => void;
  onCancel: () => void;
}

// Modal namespace picker. Mounting it *is* opening it — ScopePanel renders it
// conditionally — so the draft can be seeded from props on mount and needs no
// open/close prop. See docs/namespace-picker.md.
export function NamespacePicker({ available, selected, onDone, onCancel }: Props) {
  const [draft, setDraft] = useState<Set<string>>(() => new Set(selected));
  const [filter, setFilter] = useState("");

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return available.filter((n) => !q || n.toLowerCase().includes(q));
  }, [available, filter]);

  const selectedVisible = visible.filter((n) => draft.has(n)).length;
  const allVisible = visible.length > 0 && selectedVisible === visible.length;
  const someVisible = selectedVisible > 0 && !allVisible;

  // Checkboxes have no `indeterminate` prop — it's a DOM-only property.
  const allRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (allRef.current) allRef.current.indeterminate = someVisible;
  }, [someVisible]);

  // Esc discards, matching the backdrop and Cancel. Bound to the document so it
  // works wherever focus happens to be inside the dialog.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const toggle = (ns: string) => {
    setDraft((prev) => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns);
      else next.add(ns);
      return next;
    });
  };

  // Acts on the filtered rows only: selections hidden by the filter are never
  // touched, so you can narrow, take a batch, narrow again and add more.
  const toggleVisible = () => {
    setDraft((prev) => {
      const next = new Set(prev);
      if (allVisible) for (const n of visible) next.delete(n);
      else for (const n of visible) next.add(n);
      return next;
    });
  };

  const filtering = filter.trim().length > 0;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      {/* Backdrop: clicking it discards, like Cancel. */}
      <button
        type="button"
        aria-label="Close"
        onClick={onCancel}
        className="absolute inset-0 cursor-default bg-slate-900/40"
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="ns-picker-title"
        className="relative flex max-h-[80vh] w-full max-w-3xl flex-col rounded-lg bg-white shadow-xl"
      >
        <div className="flex items-start justify-between border-b border-slate-200 px-4 py-3">
          <div>
            <h2
              id="ns-picker-title"
              className="text-sm font-semibold text-slate-900"
            >
              Select namespaces
            </h2>
            <p className="text-xs text-slate-500">
              {draft.size} of {available.length} selected
            </p>
          </div>
          <button
            type="button"
            onClick={onCancel}
            aria-label="Close namespace picker"
            className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-slate-400 hover:bg-slate-100 hover:text-slate-700"
          >
            ✕
          </button>
        </div>

        <div className="border-b border-slate-200 px-4 py-3">
          <input
            type="text"
            // Focused on open so the dialog can be driven straight from the keyboard.
            autoFocus
            placeholder="Filter…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-full rounded border border-slate-300 px-2 py-1 text-sm outline-none focus:border-slate-500"
          />
          <label className="mt-3 flex cursor-pointer items-center gap-2 text-sm font-medium text-slate-700">
            <input
              ref={allRef}
              type="checkbox"
              checked={allVisible}
              onChange={toggleVisible}
            />
            <span>
              Select all ({visible.length}
              {filtering ? " matching" : ""})
            </span>
          </label>
        </div>

        {/* Columns are what remove the scrolling; max-height keeps a 200-namespace
            cluster from overflowing the screen rather than promising it never
            scrolls. */}
        <div className="grid flex-1 grid-cols-2 gap-x-4 overflow-y-auto px-4 py-3 sm:grid-cols-3 lg:grid-cols-4">
          {visible.map((ns) => (
            <label
              key={ns}
              title={ns}
              className="flex cursor-pointer items-center gap-2 rounded px-1 py-1 text-sm hover:bg-slate-100"
            >
              <input
                type="checkbox"
                checked={draft.has(ns)}
                onChange={() => toggle(ns)}
              />
              <span className="truncate">{ns}</span>
            </label>
          ))}
          {visible.length === 0 && (
            <p className="col-span-full text-xs text-slate-400">
              No namespaces match “{filter.trim()}”.
            </p>
          )}
        </div>

        <div className="flex justify-end gap-2 border-t border-slate-200 px-4 py-3">
          <button
            type="button"
            onClick={onCancel}
            className="rounded border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
          >
            Cancel
          </button>
          {/* Enabled even at zero: clearing the scope is a legitimate edit, and
              Run discovery keeps its own guard against an empty run. */}
          <button
            type="button"
            onClick={() => onDone(draft)}
            className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white"
          >
            Done ({draft.size})
          </button>
        </div>
      </div>
    </div>
  );
}
