import type { Snapshot } from "../types/graph";

interface Props {
  snapshot: Snapshot | null | undefined;
}

export function Header({ snapshot }: Props) {
  return (
    <header className="flex h-14 items-center justify-between border-b border-slate-200 bg-white px-4">
      <div className="flex items-baseline gap-3">
        <h1 className="text-lg font-semibold text-slate-900">kscope</h1>
        <span className="text-xs text-slate-500">
          kubernetes resource visualizer
        </span>
      </div>
      <div className="text-xs text-slate-600">
        {snapshot ? (
          <>
            <span className="font-medium text-slate-900">
              Last run: {new Date(snapshot.timestamp).toLocaleString()}
            </span>
            <span className="mx-2 text-slate-300">·</span>
            <span>
              namespaces: {snapshot.scope.namespaces.join(", ") || "—"}
            </span>
            <span className="mx-2 text-slate-300">·</span>
            <span>
              {snapshot.nodes.length} nodes, {snapshot.edges.length} edges
            </span>
          </>
        ) : (
          <span className="text-slate-400">no snapshot yet</span>
        )}
      </div>
    </header>
  );
}
