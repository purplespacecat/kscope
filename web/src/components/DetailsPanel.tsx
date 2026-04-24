import type { GraphNode } from "../types/graph";

interface Props {
  node: GraphNode | null;
  onClose: () => void;
}

export function DetailsPanel({ node, onClose }: Props) {
  if (!node) return null;
  return (
    <aside className="flex h-full w-80 flex-col border-l border-slate-200 bg-white">
      <div className="flex items-center justify-between border-b border-slate-200 px-4 py-3">
        <div>
          <div className="text-xs font-semibold uppercase tracking-wide text-slate-500">
            {node.kind}
          </div>
          <div className="text-sm font-medium text-slate-900">{node.name}</div>
          {node.namespace && (
            <div className="text-xs text-slate-500">ns/{node.namespace}</div>
          )}
        </div>
        <button
          type="button"
          onClick={onClose}
          className="text-slate-400 hover:text-slate-700"
          aria-label="Close"
        >
          ×
        </button>
      </div>
      <pre className="flex-1 overflow-auto bg-slate-50 p-3 text-xs text-slate-700">
        {JSON.stringify(node, null, 2)}
      </pre>
    </aside>
  );
}
