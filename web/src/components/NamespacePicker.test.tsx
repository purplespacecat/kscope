import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { NamespacePicker } from "./NamespacePicker";

const NAMESPACES = [
  "default",
  "kube-node-lease",
  "kube-public",
  "kube-system",
  "monitoring",
];

function open(selected: string[] = [], available: string[] = NAMESPACES) {
  const onDone = vi.fn();
  const onCancel = vi.fn();
  const view = render(
    <NamespacePicker
      available={available}
      selected={new Set(selected)}
      onDone={onDone}
      onCancel={onCancel}
    />,
  );
  /** Re-render with new props, as ScopePanel does when a snapshot lands. */
  const update = (next: { selected?: string[]; available?: string[] }) =>
    view.rerender(
      <NamespacePicker
        available={next.available ?? available}
        selected={new Set(next.selected ?? selected)}
        onDone={onDone}
        onCancel={onCancel}
      />,
    );
  return { onDone, onCancel, update, user: userEvent.setup() };
}

/** What onDone received, order-insensitive. */
const committed = (onDone: ReturnType<typeof vi.fn>) =>
  [...(onDone.mock.calls[0][0] as Set<string>)].sort();

const selectAll = () => screen.getByRole("checkbox", { name: /select all/i });
const row = (ns: string) => screen.getByRole("checkbox", { name: ns });

/** Mirrors how ScopePanel mounts the picker: a real trigger the user clicks. */
function Harness() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Namespaces
      </button>
      {open && (
        <NamespacePicker
          available={NAMESPACES}
          selected={new Set()}
          onDone={() => setOpen(false)}
          onCancel={() => setOpen(false)}
        />
      )}
    </>
  );
}

describe("NamespacePicker", () => {
  it("returns focus to the control that opened it", async () => {
    // Without this, closing leaves focus on a removed element, so it falls back
    // to <body> and the next Tab restarts from the top of the document.
    const user = userEvent.setup();
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Namespaces" });

    await user.click(trigger);
    expect(screen.getByPlaceholderText("Filter…")).toHaveFocus();

    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(trigger).toHaveFocus();
  });

  it("selects every namespace when select-all is clicked unfiltered", async () => {
    const { onDone, user } = open();

    await user.click(selectAll());
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual([...NAMESPACES].sort());
  });

  it("takes only the matching rows when filtered, leaving hidden selections intact", async () => {
    // This is the case the filtered-select-all semantics exist for: monitoring is
    // selected but filtered out of view, and must survive untouched.
    const { onDone, user } = open(["monitoring"]);

    await user.type(screen.getByPlaceholderText("Filter…"), "kube");
    await user.click(selectAll());
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual([
      "kube-node-lease",
      "kube-public",
      "kube-system",
      "monitoring",
    ]);
  });

  it("clears only the visible rows when select-all is unchecked", async () => {
    const { onDone, user } = open(NAMESPACES);

    await user.type(screen.getByPlaceholderText("Filter…"), "kube");
    await user.click(selectAll()); // all three visible are selected → clear them
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual(["default", "monitoring"]);
  });

  it("reports the count of matching rows while filtered", async () => {
    const { user } = open();
    expect(selectAll()).toHaveAccessibleName(/5/);

    await user.type(screen.getByPlaceholderText("Filter…"), "kube");
    expect(selectAll()).toHaveAccessibleName(/3 matching/);
  });

  it("is indeterminate when only some visible rows are selected", async () => {
    const { user } = open(["kube-system"]);

    expect((selectAll() as HTMLInputElement).indeterminate).toBe(true);
    expect((selectAll() as HTMLInputElement).checked).toBe(false);

    // Selecting the rest flips it to a plain checked state.
    await user.click(row("default"));
    await user.click(row("kube-node-lease"));
    await user.click(row("kube-public"));
    await user.click(row("monitoring"));
    expect((selectAll() as HTMLInputElement).indeterminate).toBe(false);
    expect((selectAll() as HTMLInputElement).checked).toBe(true);
  });

  it("discards the draft on cancel", async () => {
    const { onDone, onCancel, user } = open(["default"]);

    await user.click(row("monitoring"));
    await user.click(screen.getByRole("button", { name: /cancel/i }));

    expect(onCancel).toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
  });

  it("discards the draft on Escape", async () => {
    const { onDone, onCancel, user } = open();

    await user.click(row("default"));
    await user.keyboard("{Escape}");

    expect(onCancel).toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
  });

  it("drops namespaces that no longer exist on the cluster", async () => {
    // A saved scope can name a namespace that has since been deleted. It gets no
    // row, so select-all can't reach it and nothing else can either — and the
    // server echoes the requested scope back into every snapshot, so it would
    // re-hydrate forever.
    const { onDone, user } = open(["monitoring", "deleted-last-week"]);

    expect(screen.getByText("1 of 5 selected")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /done/i }));
    expect(committed(onDone)).toEqual(["monitoring"]);
  });

  it("adopts a newer selection that arrives before the user edits anything", async () => {
    // available and the snapshot are independent fetches: open the picker in the
    // window between them and the draft would be seeded empty, so a plain Done
    // would wipe the scope the snapshot then hydrated.
    const { onDone, update, user } = open([]);

    update({ selected: ["kube-system", "monitoring"] });
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual(["kube-system", "monitoring"]);
  });

  it("keeps in-progress edits when a newer selection arrives", async () => {
    // The flip side: once the user has touched the draft, their work wins over a
    // late-arriving snapshot rather than being silently replaced.
    const { onDone, update, user } = open([]);

    await user.click(row("default"));
    update({ selected: ["kube-system", "monitoring"] });
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual(["default"]);
  });

  it("commits an empty selection, since clearing the scope is a valid edit", async () => {
    const { onDone, user } = open(["default"]);

    await user.click(row("default"));
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual([]);
  });
});
