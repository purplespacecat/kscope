# Focus anchoring — keep the clicked node put across a reflow

Status: **designed**, not yet implemented.

## 1. Problem

Clicking in the graph rearranges the graph, and the thing you clicked ends up
somewhere else. You then hunt for it. Two distinct interactions cause this, for
different reasons:

**A. Clicking a resource card.** `App.select` sets a new `selectedId`,
`focusSubgraph` rebuilds the subgraph around it, and `<ReactFlow>` remounts
(`key={selectedId}`) so `fitView` reframes the whole thing at a new zoom. The
node *is* selected and *is* the new focus root — but the viewport jumped, so
it is no longer where the eye left it.

**B. Clicking a collapsed kind-group card** ("Pods `12` ▸"). The handler
toggles `expandedGroups` and returns early. Selection never changes, so there
is no remount and the viewport is preserved — but dagre re-lays-out everything
underneath it. Worse, the clicked card is not moved but *destroyed*: `gid`
becomes a container `gid` plus a brand-new header card `${gid}__h`. Nothing is
highlighted, and the group that just opened can land off-screen.

## 2. Intent

The node you clicked keeps its **exact screen position and zoom**; the rest of
the graph reflows around it. The cursor never leaves the node it clicked, so
the node cannot be lost. Newly revealed children may extend past the viewport
edge — that is the accepted cost, and `Ctrl-0` / **Re-center** remains the
escape hatch.

This applies only to clicks originating **in the canvas**. Selections arriving
from elsewhere — the tree panel, a relationship link in the details panel, the
k9s `Shift-G` handoff, and the initial `?focus=` URL — have no "same pixel" to
preserve and keep today's fit-the-new-subgraph behaviour unchanged.

## 3. Approach

Post-layout viewport compensation: record where the anchor node was *before*
the state change, find where it landed *after*, and pan the viewport by the
inverse of that delta.

Two approaches were rejected. *Capture-and-restore around the remount* uses
the same math but races `fitView` on mount, needing the double-`rAF` dance
`RecenterButton` already resorts to — fragile for no gain. *Layout-stable
expansion* (rendering expanded groups as overlays that don't displace rank
neighbours) needs no compensation at all, but rewrites the most intricate part
of `layout()` and only addresses case B.

## 4. Mechanism

### 4.1 Anchor lifecycle

One piece of state in `GraphCanvas`, set by the click handler, consumed by an
effect, then cleared:

```ts
type Anchor = { id: string; pos: { x: number; y: number } };
```

`id` is the node to look for **after** the reflow; `pos` is the clicked node's
absolute position **before** it. The two can describe different nodes: for a
group card the anchor id is the successor of §4.3, while `pos` is always
measured from the card actually clicked.

State, not a ref, because it must be readable during the render that follows
the click — `GraphCanvas` already documents that refs cannot be written during
render. React batches `setAnchor` with the `selectedId` update from
`onSelect`, so both land in a single render pass.

### 4.2 Absolute positions

`layout()` gains a third return value, `absPos: Map<string, {x, y}>`.

Required because container members carry positions *relative* to their parent
(`parentId` + `extent: "parent"`), so raw `node.position` values are not
comparable across a reflow. Absolute position is container origin + slot
offset, both of which `layout()` already computes — so this needs no xyflow
internals API and `layout()` stays pure.

### 4.3 Successor rule

Expanding a group swaps node identity, so the anchor id is not always the
clicked id:

| Clicked | Anchor id |
| --- | --- |
| collapsed group card `gid` | `${gid}__h` (expanded header) |
| expanded header `${gid}__h` | `gid` (collapsed card) |
| resource node | unchanged — ids are stable |

Anchoring a group card on `gid` alone would latch onto the *container's*
top-left corner, so the card would visibly shift by the container padding.

### 4.4 Viewport math

Screen position of a flow point `p` is `p * zoom + vp`. Holding it constant:

```
p_old * z + vp_old = p_new * z + vp_new
    ⇒  vp_new = vp_old − (p_new − p_old) * z
```

Pan by the inverse of the movement, scaled by zoom. Zoom is never touched.

No `requestAnimationFrame` is needed — unlike `RecenterButton`, this does not
wait on measurement, because the coordinates come from our own layout and are
already known at render time.

The effect lives in a small child component inside `<ReactFlow>`, since
`useReactFlow()` requires that context — the same reason `RecenterButton` is a
child rather than inline in `GraphCanvas`.

If the anchor id cannot be resolved after the reflow, the effect is a no-op and
the anchor clears: the viewport stays put rather than jumping somewhere
arbitrary. The successor rule should make this unreachable; it is a safe
failure, not an expected path.

### 4.5 Anchored vs refit

`key={selectedId ?? "root"}` becomes `key={viewKey}`, a counter bumped **only**
for refit transitions, detected with the render-time pattern `GraphCanvas`
already uses for tooltip dismissal:

```ts
if (selectedId !== seenSelectedId) {
  setSeenSelectedId(selectedId);
  if (!anchor) setViewKey((k) => k + 1);   // arrived from outside → refit
  if (tip) setTip(null);
}
```

| Path | anchor | viewKey | Result |
| --- | --- | --- | --- |
| Canvas click, resource | set | unchanged | no remount, anchored |
| Canvas click, group card | set | unchanged | `selectedId` never changes; anchored |
| Tree / details / k9s / `?focus=` | null | bumped | remount + `fitView`, as today |

## 5. Knock-on: `RecenterButton`'s home viewport

`home` is currently recaptured on every selection change, because the remount
destroys the component. Once anchored transitions stop remounting, `home` can
outlive the subgraph it was measured against: `recenter()` calls `fitView()`,
lands on a viewport that does not match the stale `home`, and the button never
hides again.

Fix: recapture `home` once the recenter animation settles. This regression is
introduced by removing the guaranteed remount, so it is in scope here.

## 6. Non-goals

- **Group expansion does not change selection.** Group cards are not
  resources: `DetailsPanel` would have nothing to show, and selection drives
  `focusSubgraph`, which would rebuild the whole view and defeat the purpose.
  Anchoring alone delivers "don't lose it".
- **No change to `focusSubgraph`, the node budget, or the `+N` behaviour.**
- **No change to the k9s handoff, the shareable-URL path, or the tree panel.**

## 7. Testing

Pure functions in a new `web/src/lib/viewport.ts`, unit-tested in
`viewport.test.ts` — mirroring the existing `lib/display.ts` / `display.test.ts`
pairing:

- the compensation math of §4.4, including a non-1.0 zoom;
- the successor mapping of §4.3, both directions;
- `absPos` for a node nested inside a container, where relative and absolute
  positions differ.

That is where the risk of being wrong actually lives.

**Deliberately not covered:** component-level interaction tests for the canvas.
`GraphCanvas` is mocked out of the current suite because jsdom lacks
`ResizeObserver`/`DOMMatrix`; polyfilling that to assert on viewport transforms
is a larger job than this fix and belongs in its own change.

## 8. Files touched

| File | Change |
| --- | --- |
| `web/src/lib/viewport.ts` | new — pure compensation math + successor mapping |
| `web/src/lib/viewport.test.ts` | new — unit tests for the above |
| `web/src/components/GraphCanvas.tsx` | `absPos` from `layout()`, anchor state, anchor-applying child component, `viewKey` replacing the `selectedId` remount key, `RecenterButton` home recapture |

`App.tsx` is unchanged: origin detection lives entirely in `GraphCanvas`, which
already knows whether a click came from its own canvas.
