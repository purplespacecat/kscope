# Namespace picker — a modal with select-all

Status: **designed**, not yet implemented.

## 1. Problem

Namespaces are chosen from a scrolling checkbox list inside the Scope section of
the sidebar. The section is capped at `max-h-[45%]` so the resource tree keeps
room, which leaves the list a short window — on a cluster with a couple of dozen
namespaces you scroll a small bar to find anything, and you cannot see the whole
set at once. There is also no way to select everything short of clicking every
row.

The filter box sits above the list, in the sidebar, where it takes vertical space
from the very list it filters.

## 2. Intent

- **Select all** as the first row of the namespace list.
- The list moves to a **surface wide enough to show it without scrolling**.
- The **filter moves with it**.

## 3. Decisions

| Decision | Choice | Why |
| --- | --- | --- |
| Surface | Centered modal | Width is what removes the scrolling: a wide surface lays namespaces out in columns, so 40+ are visible at once. An anchored popover stays near the sidebar and would still scroll after ~12 rows; an inline expand is stuck at the sidebar's 18rem and stays single-column. |
| Commit | `Done` / `Cancel` | Editing a draft and committing on `Done` makes `Cancel` meaningful. |
| Select-all scope | The filtered rows | Acts on exactly what is visible. Filter `kube`, take those four, filter again, add more — selections hidden by the filter are never touched. Acting on all 24 while four are shown makes the Run discovery count jump in a way the user did not ask for. |
| Select-all control | Tri-state checkbox | One row that both acts and reports: checked when every visible row is selected, indeterminate when some are. |

## 4. Layout

`ScopePanel` keeps the context select, the infra/CRD toggles and Run discovery.
The filter input and the checkbox list move into the modal. In their place: a
trigger button and a bounded, read-only summary of the current selection.

The trigger reads `Namespaces · 4 of 24` and opens the modal. Beneath it, the
summary lists the first **three** selected names plus `+N more`, or
`None selected` when empty.

The summary is **not** decoration. Today the sidebar lists every namespace with
its checkbox, so the scope is visible at a glance; a modal hides that. Capping it
at three names keeps the section from growing tall on a select-all.

With the scrolling list gone, the `max-h-[45%]` cap on the section is no longer
needed, so the resource tree below gains space.

## 5. Modal mechanics

- `position: fixed inset-0` with a backdrop at `z-50`; App's own overlays sit at
  `z-20`. No portal: nothing in the sidebar's ancestry creates a containing
  block, so `fixed` escapes the sidebar's overflow on its own.
- Namespaces in a grid: 2 columns by default, 3 at `sm`, 4 at `lg`, with a
  `max-height` and overflow as a safety valve — a 200-namespace cluster degrades
  to scrolling rather than overflowing the screen. "No scrolling" is the goal for
  ordinary clusters, not a guarantee for every one.
- Esc, backdrop click, `✕` and `Cancel` all close **and discard**. `Done` commits.
- `Done` is always enabled, including with nothing selected — clearing the scope
  is a legitimate edit. Run discovery keeps its own `selected.size > 0` guard, so
  an empty selection blocks the run, not the edit.
- The filter autofocuses on open, so the modal can be driven straight from the
  keyboard.
- `role="dialog"`, `aria-modal="true"`, labelled by its heading.

## 6. State ownership

`selected` stays in `ScopePanel` — it is what Run discovery sends. The modal
receives it as a prop, copies it into a **draft** on open, mutates only the
draft, and returns it on `Done`. That is what makes `Cancel` work, and it keeps
the modal a pure `(available, selected) → selected` component with no shared
mutable state.

`filter` becomes modal-local and resets on each open, which also removes the
`setFilter("")` line from `onContextChange`.

## 7. Select-all semantics

Derived from the visible (filtered) rows:

| Visible rows selected | Checkbox | Clicking it |
| --- | --- | --- |
| all | checked | removes exactly those visible |
| none | unchecked | adds all visible |
| some | indeterminate | adds all visible |

`indeterminate` is not a React prop, so it is set through a ref. The label reads
`Select all (24)` unfiltered and `Select all (4 matching)` when filtered.

**"All" is a snapshot, not a standing instruction.** `Scope.namespaces` is a
`[]string` with no wildcard, so select-all materialises the names that exist at
that moment. A namespace created afterwards is not included until select-all is
used again. Keeping it explicit — rather than adding an "all namespaces" flag —
also keeps invocations parameterised rather than implicit.

## 8. Non-goals

- No change to `Scope`, the discovery API, or anything server-side.
- The context select, infra/CRD toggles and Run discovery button stay where they
  are and behave as they do now.
- Selection still resets when the context changes: namespace names are
  cluster-specific, and a kept selection would silently scope the next run to
  namespaces that may not exist on the new cluster.

## 9. Testing

Unlike the graph canvas, this is plain DOM — no `@xyflow/react`, so jsdom handles
it properly and the component is testable for real. `ScopePanel` has no tests
today; the picker gets its own, driven through props:

- select-all with no filter selects every namespace;
- select-all with a filter selects only the matching rows **and leaves a
  selection hidden by the filter intact** — the case the chosen semantics exist
  for;
- the checkbox is indeterminate when only some visible rows are selected;
- `Cancel` discards a draft change; `Done` commits it;
- Esc closes and discards.

## 10. Files touched

| File | Change |
| --- | --- |
| `web/src/components/NamespacePicker.tsx` | new — the modal: filter, tri-state select-all, grid of namespaces, Done/Cancel |
| `web/src/components/NamespacePicker.test.tsx` | new — the behaviours in §9 |
| `web/src/components/ScopePanel.tsx` | filter input, filter state and checkbox list removed (including the `setFilter("")` in `onContextChange`); trigger button + selection summary added; `max-h-[45%]` cap dropped |
