import { useState } from "react";
import { Header } from "./components/Header";
import { ScopePanel } from "./components/ScopePanel";
import { GraphCanvas } from "./components/GraphCanvas";
import { DetailsPanel } from "./components/DetailsPanel";
import { useLatest } from "./hooks/useGraph";
import type { GraphNode } from "./types/graph";

export default function App() {
  const { data: snapshot, isLoading, error } = useLatest();
  const [selected, setSelected] = useState<GraphNode | null>(null);

  return (
    <div className="flex h-full flex-col">
      <Header snapshot={snapshot} />
      <div className="flex min-h-0 flex-1">
        <ScopePanel snapshot={snapshot} />
        <main className="relative flex-1 bg-slate-100">
          {isLoading && (
            <Centered>
              <span className="text-sm text-slate-500">Loading…</span>
            </Centered>
          )}
          {error && (
            <Centered>
              <span className="text-sm text-red-600">
                Failed to load: {(error as Error).message}
              </span>
            </Centered>
          )}
          {!isLoading && !error && !snapshot && (
            <Centered>
              <div className="max-w-sm text-center text-sm text-slate-500">
                No snapshot yet. Pick one or more namespaces on the left and
                click <b>Run discovery</b>.
              </div>
            </Centered>
          )}
          {snapshot && (
            <GraphCanvas snapshot={snapshot} onSelect={setSelected} />
          )}
        </main>
        <DetailsPanel node={selected} onClose={() => setSelected(null)} />
      </div>
    </div>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full w-full items-center justify-center">
      {children}
    </div>
  );
}
