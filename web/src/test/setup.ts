// Vitest setup, loaded before every test file (vite.config.ts `test.setupFiles`).
//
// The jest-dom import does two jobs: registers the DOM matchers
// (toBeInTheDocument, ...) on vitest's expect at runtime, and — because this
// file is part of the tsconfig program — its module augmentation makes those
// matchers type-check in every test file.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Unmount React trees between tests. RTL does this automatically only when
// its afterEach hook is injected via globals, which we don't enable.
afterEach(cleanup);

// jsdom's HTMLDialogElement implements only the `open` property — no showModal,
// no close, no top layer. Its UA stylesheet hides `dialog:not([open])`, so
// without this a modal's contents aren't queryable at all. Shimmed here rather
// than branched on in the component, so production code calls showModal()
// unconditionally.
//
// Faithful to the spec where it matters: showModal() on a dialog that is already
// open returns instead of throwing, which is what makes StrictMode's
// double-invoked mount effects safe.
const dialogProto = globalThis.HTMLDialogElement?.prototype;
if (dialogProto && !dialogProto.showModal) {
  dialogProto.showModal = function showModal(this: HTMLDialogElement) {
    if (this.hasAttribute("open")) return;
    this.setAttribute("open", "");
  };
  dialogProto.close = function close(this: HTMLDialogElement) {
    this.removeAttribute("open");
  };
}
