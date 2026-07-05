import { useEffect, useMemo, useState } from "react";
import type { GraphEdge, GraphNode } from "../types/graph";
import { useManifest } from "../hooks/useGraph";
import {
  HEALTH_DOT,
  HEALTH_LABEL,
  health,
  incomingEdgeLabel,
  kindAbbrev,
} from "../lib/display";

interface Props {
  node: GraphNode | null;
  byId: Map<string, GraphNode>;
  edges: GraphEdge[];
  snapshotTs?: string;
  onSelect: (node: GraphNode) => void;
  onClose: () => void;
}

interface RelatedRow {
  key: string;
  label: string; // "mounts" (outgoing) or "mounted by" (incoming)
  other: GraphNode;
}

export function DetailsPanel({
  node,
  byId,
  edges,
  snapshotTs,
  onSelect,
  onClose,
}: Props) {
  // Hooks run unconditionally (rules of hooks); the null guard comes after.
  const manifest = useManifest(node?.id ?? null, snapshotTs);

  const ancestry = useMemo(() => {
    if (!node) return [];
    const chain: GraphNode[] = [];
    let cur = node.parentId ? byId.get(node.parentId) : undefined;
    while (cur) {
      chain.unshift(cur);
      cur = cur.parentId ? byId.get(cur.parentId) : undefined;
    }
    return chain;
  }, [node, byId]);

  const related = useMemo<RelatedRow[]>(() => {
    if (!node) return [];
    const rows: RelatedRow[] = [];
    for (const e of edges) {
      if (e.source === node.id) {
        const other = byId.get(e.target);
        if (other) rows.push({ key: e.id, label: e.kind, other });
      } else if (e.target === node.id) {
        const other = byId.get(e.source);
        if (other)
          rows.push({ key: e.id, label: incomingEdgeLabel(e.kind), other });
      }
    }
    return rows;
  }, [node, edges, byId]);

  if (!node) return null;
  const labels = Object.entries(node.labels ?? {});

  return (
    <aside className="flex h-full w-96 flex-col border-l border-slate-200 bg-white">
      <div className="flex items-start justify-between border-b border-slate-200 px-4 py-3">
        <div className="min-w-0">
          <div className="text-xs font-semibold uppercase tracking-wide text-slate-500">
            {node.kind}
            {node.apiVersion && (
              <span className="ml-1.5 font-normal normal-case text-slate-400">
                {node.apiVersion}
              </span>
            )}
          </div>
          <div
            className="truncate text-sm font-medium text-slate-900"
            title={node.name}
          >
            {node.name}
          </div>
          {ancestry.length > 0 && (
            <div className="mt-0.5 flex flex-wrap items-center gap-x-1 text-[11px] text-slate-400">
              {ancestry.map((a) => (
                <span key={a.id} className="flex items-center gap-x-1">
                  <button
                    type="button"
                    onClick={() => onSelect(a)}
                    className="max-w-32 truncate hover:text-slate-700 hover:underline"
                    title={`${a.kind}: ${a.name}`}
                  >
                    {a.name}
                  </button>
                  <span>/</span>
                </span>
              ))}
              <span className="text-slate-500">{node.name}</span>
            </div>
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

      <div className="flex-1 space-y-4 overflow-y-auto p-4">
        <section>
          <SectionTitle>Health</SectionTitle>
          <span className="inline-flex items-center gap-1.5 rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-700">
            <span
              className={`h-2 w-2 rounded-full ${HEALTH_DOT[health(node)]}`}
            />
            {HEALTH_LABEL[health(node)]}
          </span>
        </section>

        {node.kubectl && (
          <section>
            <SectionTitle>Retrieve with kubectl</SectionTitle>
            <div className="flex items-start gap-1.5">
              <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap rounded bg-slate-900 px-2 py-1.5 text-[11px] text-slate-100">
                {node.kubectl}
              </code>
              <CopyButton text={node.kubectl} />
            </div>
          </section>
        )}

        {related.length > 0 && (
          <section>
            <SectionTitle>Relationships</SectionTitle>
            <div className="space-y-0.5">
              {related.map((r) => (
                <button
                  key={r.key}
                  type="button"
                  onClick={() => onSelect(r.other)}
                  className="flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-left text-xs hover:bg-slate-50"
                  title={`${r.other.kind}: ${r.other.name}`}
                >
                  <span className="w-24 shrink-0 text-[10px] uppercase tracking-wide text-slate-400">
                    {r.label}
                  </span>
                  <span className="shrink-0 rounded bg-slate-100 px-1 text-[10px] font-semibold text-slate-500">
                    {kindAbbrev(r.other.kind)}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-slate-700">
                    {r.other.name}
                  </span>
                  <span
                    className={`h-1.5 w-1.5 shrink-0 rounded-full ${HEALTH_DOT[health(r.other)]}`}
                  />
                </button>
              ))}
            </div>
          </section>
        )}

        {labels.length > 0 && (
          <section>
            <SectionTitle>Labels</SectionTitle>
            <div className="flex flex-wrap gap-1">
              {labels.map(([k, v]) => (
                <span
                  key={k}
                  className="max-w-full truncate rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600"
                  title={`${k}=${v}`}
                >
                  {k}={v}
                </span>
              ))}
            </div>
          </section>
        )}

        {!node.synthetic && node.kind !== "Cluster" && (
          <section>
            <div className="mb-1.5 flex items-center justify-between">
              <SectionTitle noMargin>Manifest</SectionTitle>
              {manifest.data && <CopyButton text={manifest.data} />}
            </div>
            {manifest.isLoading && (
              <p className="text-xs text-slate-400">Loading…</p>
            )}
            {manifest.isError && (
              <p className="text-xs text-slate-400">
                {(manifest.error as Error).message}
              </p>
            )}
            {manifest.data && (
              <pre className="max-h-96 overflow-auto rounded bg-slate-900 p-2 text-[11px] leading-relaxed text-slate-100">
                {manifest.data}
              </pre>
            )}
          </section>
        )}

        <details className="text-xs">
          <summary className="cursor-pointer text-slate-400 hover:text-slate-600">
            Raw node
          </summary>
          <pre className="mt-2 overflow-x-auto rounded bg-slate-50 p-2 text-[11px] text-slate-700">
            {JSON.stringify(node, null, 2)}
          </pre>
        </details>
      </div>
    </aside>
  );
}

function SectionTitle({
  children,
  noMargin,
}: {
  children: React.ReactNode;
  noMargin?: boolean;
}) {
  return (
    <div
      className={`text-xs font-semibold uppercase tracking-wide text-slate-400 ${noMargin ? "" : "mb-1.5"}`}
    >
      {children}
    </div>
  );
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(() => setCopied(true));
      }}
      className="shrink-0 rounded border border-slate-300 px-2 py-1 text-xs text-slate-600 hover:bg-slate-100"
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}
