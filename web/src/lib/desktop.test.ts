import { afterEach, describe, expect, it, vi } from "vitest";
import { onExternalLinkClick } from "./desktop";

describe("onExternalLinkClick", () => {
  afterEach(() => {
    delete window.runtime;
  });

  it("does nothing in a browser, leaving the anchor to navigate", () => {
    const e = { preventDefault: vi.fn() };
    onExternalLinkClick(e, "https://github.com/x/y");
    expect(e.preventDefault).not.toHaveBeenCalled();
  });

  it("routes to the OS browser in the Wails webview", () => {
    // In the webview a target=_blank anchor silently does nothing (no
    // new-window handler), so the handler must take over completely.
    const open = vi.fn();
    window.runtime = {
      EventsOn: vi.fn(() => () => {}),
      BrowserOpenURL: open,
    };

    const e = { preventDefault: vi.fn() };
    onExternalLinkClick(e, "https://github.com/x/y");

    expect(e.preventDefault).toHaveBeenCalled();
    expect(open).toHaveBeenCalledWith("https://github.com/x/y");
  });
});
