import { useEffect, useMemo, useRef, useState } from "react";

interface Props {
  available: string[];
  /** Committed selection; copied into a draft so Cancel can discard. */
  selected: Set<string>;
  onDone: (next: Set<string>) => void;
  onCancel: () => void;
}

/**
 * The selection narrowed to namespaces that actually exist on the cluster.
 *
 * A saved scope can name a namespace that has since been deleted: the server
 * filters unknown names out of discovery but echoes the *requested* scope back
 * into the snapshot, so the name re-hydrates on every load. Such a name has no
 * row here, which means no control — not its checkbox, not select-all — could
 * ever clear it. Dropping it on the way in and out is what makes the counts
 * honest and the scope editable.
 */
const onCluster = (available: string[], selected: Set<string>) =>
  new Set(available.filter((n) => selected.has(n)));

// Modal namespace picker. Mounting it *is* opening it — ScopePanel renders it
// conditionally — so the draft is seeded from props on mount and there is no
// open/close prop. See docs/namespace-picker.md.
export function NamespacePicker({ available, selected, onDone, onCancel }: Props) {
  const [draft, setDraft] = useState(() => onCluster(available, selected));
  const [edited, setEdited] = useState(false);
  const [filter, setFilter] = useState("");

  // ScopePanel re-hydrates `selected` from every arriving snapshot, and one can
  // land while this is open — `available` and the snapshot are independent
  // fetches, so opening in the window between them would otherwise seed an empty
  // draft that a plain Done would commit over the real scope. Adopt a newer
  // selection only while the draft is untouched; after that the user's edits win.
  const [seenSelected, setSeenSelected] = useState(selected);
  if (selected !== seenSelected) {
    setSeenSelected(selected);
    if (!edited) setDraft(onCluster(available, selected));
  }

  // Captured in a state initialiser, which runs during the first render — an
  // effect would be too late, because React calls `autoFocus` in the commit
  // phase and would have already moved focus into the filter input. Restoring on
  // unmount is necessary because the browser's own restore only runs on close().
  const [opener] = useState(() => document.activeElement as HTMLElement | null);
  useEffect(() => () => opener?.focus?.(), [opener]);

  const dialogRef = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = dialogRef.current;
    // showModal() brings the focus trap, ::backdrop and top-layer stacking with
    // it — the last of which matters because the graph's hover tooltip is also
    // z-50 and would otherwise paint over this on tree order.
    //
    // The `open` check is for StrictMode, which double-invokes mount effects:
    // calling showModal() on a dialog that is already modal is a no-op per spec,
    // but on one merely marked `open` it throws InvalidStateError.
    if (!dialog || dialog.open) return;
    dialog.showModal();
  }, []);

  // Escape is handled here rather than left to the dialog's own `cancel` event so
  // it behaves identically under jsdom. Both paths funnel into onCancel, which is
  // idempotent.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return available.filter((n) => !q || n.toLowerCase().includes(q));
  }, [available, filter]);

  const allVisible = visible.length > 0 && visible.every((n) => draft.has(n));
  const someVisible = !allVisible && visible.some((n) => draft.has(n));

  // Checkboxes have no `indeterminate` prop — it's a DOM-only property.
  const allRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (allRef.current) allRef.current.indeterminate = someVisible;
  }, [someVisible]);

  const toggle = (ns: string) => {
    setEdited(true);
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
    setEdited(true);
    setDraft((prev) => {
      const next = new Set(prev);
      if (allVisible) for (const n of visible) next.delete(n);
      else for (const n of visible) next.add(n);
      return next;
    });
  };

  const filtering = filter.trim().length > 0;

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby="ns-picker-title"
      // A click landing on the dialog itself is a click on the backdrop, since
      // all content sits in the padded child below.
      onClick={(e) => {
        if (e.target === dialogRef.current) onCancel();
      }}
      onCancel={(e) => {
        e.preventDefault(); // React owns open/closed; don't let the UA close it
        onCancel();
      }}
      className="m-auto max-h-[80vh] w-full max-w-3xl rounded-lg bg-white p-0 text-slate-900 shadow-xl backdrop:bg-slate-900/40"
    >
      <div className="flex max-h-[80vh] flex-col">
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
            title="Close"
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

        {/* Columns are what remove the scrolling; max-height keeps a
            200-namespace cluster from overflowing the screen rather than
            promising it never scrolls. */}
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
            onClick={() => onDone(onCluster(available, draft))}
            className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white"
          >
            Done ({draft.size})
          </button>
        </div>
      </div>
    </dialog>
  );
}
