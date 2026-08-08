// Adapter over the Wails desktop shell.
//
// Wails injects `window.runtime` and `window.go.<pkg>.<Struct>` into the
// webview at load time. We reach them through those globals rather than
// importing the CLI-generated `wailsjs/` bindings, because that directory only
// exists after a `wails build`/`wails dev` — importing it would break the
// plain browser build (`npm run dev`, `npm run build`) that scripts/dev.sh
// uses. Every function here degrades to a browser-native equivalent.

interface WailsRuntime {
  EventsOn(name: string, callback: (...data: unknown[]) => void): () => void;
}

interface BoundApp {
  SaveManifest(suggestedName: string, yaml: string): Promise<string>;
}

declare global {
  interface Window {
    runtime?: WailsRuntime;
    go?: { main?: { App?: BoundApp } };
  }
}

/** True when running inside the Wails webview rather than a browser tab. */
export function isDesktop(): boolean {
  return typeof window !== "undefined" && !!window.runtime;
}

/**
 * Subscribe to an event emitted by the Go side (menu commands, focus requests).
 * Returns an unsubscribe function; in the browser it's a no-op, so callers can
 * use it unconditionally in an effect cleanup.
 */
export function onDesktopEvent(name: string, callback: () => void): () => void {
  if (!window.runtime) return () => {};
  return window.runtime.EventsOn(name, callback);
}

export const DESKTOP_EVENTS = {
  refresh: "kscope:refresh",
  recenter: "kscope:recenter",
  focus: "kscope:focus",
} as const;

/**
 * A jump-to-resource request from outside the app (the k9s plugin). Either the
 * resource resolved to a node in the current snapshot, or it didn't — in which
 * case the payload carries enough to explain why.
 */
export interface FocusRequest {
  id?: string;
  missing?: boolean;
  namespace?: string;
  name?: string;
  kind?: string;
  /** Set when the request named a cluster other than the snapshot's. */
  context?: string;
}

/** Like onDesktopEvent, but for events that carry a payload. */
export function onDesktopData<T>(
  name: string,
  callback: (data: T) => void,
): () => void {
  if (!window.runtime) return () => {};
  return window.runtime.EventsOn(name, (...data: unknown[]) =>
    callback(data[0] as T),
  );
}

/**
 * Save YAML to a file. Uses a native save dialog on the desktop; falls back to
 * a browser download elsewhere.
 *
 * Resolves to the saved path on desktop, "" if the user cancelled, or null in
 * the browser (where the download is fire-and-forget and no path is knowable).
 */
export async function saveManifest(
  suggestedName: string,
  yaml: string,
): Promise<string | null> {
  const app = window.go?.main?.App;
  if (app) return app.SaveManifest(suggestedName, yaml);

  // Browser fallback: an object-URL download.
  const url = URL.createObjectURL(new Blob([yaml], { type: "application/yaml" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${suggestedName.replace(/[/\\:]/g, "-")}.yaml`;
  a.click();
  URL.revokeObjectURL(url);
  return null;
}
