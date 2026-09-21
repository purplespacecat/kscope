import type { Snapshot } from "../types/graph";

/**
 * Discovery errors are per-list failures, never fatal — a pass that could not
 * read pods still stores a snapshot and still replaces the previous map. The
 * count has to be visible or that reads as a mysteriously empty graph rather
 * than as a permissions problem.
 *
 * Repeats are collapsed the way `kscope info` collapses them: a Crossplane
 * cluster produces over a thousand near-identical rate-limiter timeouts, and
 * listing them individually says less than counting them does.
 */
function summariseErrors(errors: string[]): string {
  const count = new Map<string, number>();
  for (const e of errors) count.set(e, (count.get(e) ?? 0) + 1);
  return [...count.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, 5)
    .map(([msg, n]) => (n > 1 ? `(${n}×) ${msg}` : msg))
    .join("\n");
}

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
            {snapshot.cluster && (
              <>
                <span className="font-medium text-slate-900">
                  {snapshot.cluster.context}
                </span>
                <span className="mx-2 text-slate-300">·</span>
                <span>
                  {snapshot.cluster.version}
                  {snapshot.cluster.distro && ` (${snapshot.cluster.distro})`}
                </span>
                <span className="mx-2 text-slate-300">·</span>
              </>
            )}
            <span className="font-medium text-slate-900">
              Last run: {new Date(snapshot.timestamp).toLocaleString()}
            </span>
            <span className="mx-2 text-slate-300">·</span>
            <span>
              namespaces: {snapshot.scope.namespaces.join(", ") || "all"}
            </span>
            <span className="mx-2 text-slate-300">·</span>
            <span>
              {snapshot.nodes.length} nodes, {snapshot.edges.length} edges
            </span>
            {!!snapshot.stats?.errors?.length && (
              <>
                <span className="mx-2 text-slate-300">·</span>
                <span
                  title={summariseErrors(snapshot.stats.errors)}
                  className="cursor-help rounded bg-amber-100 px-1.5 py-0.5 font-medium text-amber-900"
                >
                  {snapshot.stats.errors.length} skipped
                </span>
              </>
            )}
          </>
        ) : (
          <span className="text-slate-400">no snapshot yet</span>
        )}
      </div>
    </header>
  );
}
