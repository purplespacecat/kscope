# Focus anchoring — keep the clicked node put across a reflow

Status: **implemented** on `fix/graph-focus-anchoring`. Pure geometry is
unit-tested; the canvas wiring is verified by use (see §7).

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

Container members carry positions *relative* to their parent (`parentId` +
`extent: "parent"`), so raw `node.position` values are not comparable across a
reflow.

`absolutePositions(flowNodes)` resolves them by accumulating each node's
position up its `parentId` chain. It reads only the flow nodes `layout()`
already returns, so `layout()`'s signature is untouched and the function is
pure and directly unit-testable — simpler than the originally designed third
return value, which would have threaded container origins through `layout()`'s
internals. A dangling `parentId` falls back to the raw position: failing to
anchor beats NaN coordinates, which would corrupt the viewport instead.

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

Which direction the toggle goes is read from the clicked card's own id
(`groupWillExpand`): a collapsed card's id *is* `gid`, an expanded header's is
`gid__h`. Deriving it from the `expandedGroups` set instead would let a rapid
second click act on a render that hasn't caught up and expand twice rather than
toggling back.

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

**Unit** — `web/src/lib/viewport.test.ts`, mirroring the existing
`lib/display.ts` / `display.test.ts` pairing:

- the compensation math of §4.4, asserted as the screen-position invariant
  rather than bare arithmetic, and at a non-1.0 zoom;
- the successor mapping and toggle direction of §4.3, both ways;
- `absolutePositions` for a nested container, a dangling `parentId`, and
  top-level passthrough.

**Component** — `web/src/components/GraphCanvas.anchor.test.tsx` renders the real
`App` with the real canvas (only `api/client` is mocked) and reads screen
positions straight off the DOM transforms:

- a clicked resource holds its screen position while its subgraph is rebuilt —
  this covers the `viewKey` decision and the batched anchor/selection update,
  the riskiest part of the design;
- a group card expands and collapses again, guarding the §4.3 toggle direction
  at DOM level;
- toggling a group card leaves the selection untouched (§6).

**Known limit.** Nodes inside a group container (`extent: "parent"`) cannot have
their geometry asserted under jsdom: xyflow measures the container as 0x0 and
clamps every member onto a single point, so member positions in the DOM are
meaningless. Stubbing `getBoundingClientRect` and the ResizeObserver
`contentRect` does not reach the code path that populates measurements. Group
*card* pixel-accuracy therefore rests on the unit-tested math plus a manual
browser check; the toggle *behaviour* is covered above.

Related observation: under jsdom the initial `fitView` never completes (nothing
is measured), so it retries on the next node update and overwrites the anchor
pan. In a real browser the fit completes at mount — but it does imply a narrow
race if a click lands before the initial fit settles. Pre-existing: `fitView` on
mount is unchanged by this work.

## 8. Files touched

| File | Change |
| --- | --- |
| `web/src/lib/viewport.ts` | new — pure compensation math + successor mapping |
| `web/src/lib/viewport.test.ts` | new — unit tests for the above |
| `web/src/components/GraphCanvas.anchor.test.tsx` | new — DOM-level anchoring/toggle tests, with the jsdom stubs xyflow needs |
| `web/src/components/GraphCanvas.tsx` | anchor state, `AnchorKeeper` child inside `<ReactFlow>`, `viewKey` replacing the `selectedId` remount key, `HEADER_SUFFIX` shared with `layout()`, `RecenterButton` home recapture |

`App.tsx` is unchanged: origin detection lives entirely in `GraphCanvas`, which
already knows whether a click came from its own canvas.
