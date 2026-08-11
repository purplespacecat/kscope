import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { NamespacePicker } from "./NamespacePicker";

const NAMESPACES = [
  "default",
  "kube-node-lease",
  "kube-public",
  "kube-system",
  "monitoring",
];

function open(selected: string[] = []) {
  const onDone = vi.fn();
  const onCancel = vi.fn();
  render(
    <NamespacePicker
      available={NAMESPACES}
      selected={new Set(selected)}
      onDone={onDone}
      onCancel={onCancel}
    />,
  );
  return { onDone, onCancel, user: userEvent.setup() };
}

/** What onDone received, order-insensitive. */
const committed = (onDone: ReturnType<typeof vi.fn>) =>
  [...(onDone.mock.calls[0][0] as Set<string>)].sort();

const selectAll = () => screen.getByRole("checkbox", { name: /select all/i });
const row = (ns: string) => screen.getByRole("checkbox", { name: ns });

describe("NamespacePicker", () => {
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

  it("commits an empty selection, since clearing the scope is a valid edit", async () => {
    const { onDone, user } = open(["default"]);

    await user.click(row("default"));
    await user.click(screen.getByRole("button", { name: /done/i }));

    expect(committed(onDone)).toEqual([]);
  });
});
