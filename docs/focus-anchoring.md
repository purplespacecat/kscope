# Focus anchoring — keep the clicked node put across a reflow

Status: **implemented** on `fix/graph-focus-anchoring`. Pure geometry is
unit-tested and the canvas wiring is covered by a DOM-level component suite;
group-container geometry remains browser-only (see §7).

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
underneath it. Worse, as the layout stood at the time, the clicked card was not
moved but *destroyed*: `gid` became a container `gid` plus a brand-new header card
`${gid}__h`. Nothing is highlighted, and the group that just opened can land
off-screen. (The card is stable now — see
[`group-expansion.md`](group-expansion.md) — but the anchoring below had to cope
with that swap.)

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
interface Anchor {
  id: string;                  // node to look for after the reflow
  pos: Point;                  // the clicked node's absolute position before it
  forSelection: string | null; // the selection this anchor belongs to
}
```

`id` is the node to look for **after** the reflow (§4.3); `pos` is its absolute
position **before** it. For a group toggle both refer to the group *card*, even
when the click landed on the members box — the card is what must not move.
`forSelection` is what distinguishes an anchored transition from a refit (§4.5).

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

The walk is iterative, not recursive, and detects a `parentId` cycle — every node
on a cycle keeps its raw position. Unreachable from today's `layout()`, where
containers carry no `parentId`, but recursion would overflow the stack and take
the canvas down, and an order-dependent break point would pan by an arbitrary
offset when the pre- and post-reflow positions resolved through different ones.

### 4.3 The card's id is stable

Every kind-group has two clickable nodes, and their ids never overlap:

| Node | id | Present when |
| --- | --- | --- |
| group card | `gid` | always |
| members box | `${gid}__m` | expanded |

Because the card keeps `gid` in both states, the anchor is simply "hold `gid`",
and its `pos` is read from the card rather than from the clicked node — so
collapsing by clicking the box background anchors on the card too, instead of
comparing two different points. Resource nodes need no mapping either; their ids
are equally stable.

Toggle **direction** comes from the `expandedGroups` set inside the functional
updater (`prev.has(gid) ? delete : add`), never from the clicked id.

This replaced an earlier design in which expanding swapped the card for a
`${gid}__h` header inside a container that *reused* `gid`. The shared id meant no
rule based on the clicked id could distinguish a collapsed card from an expanded
container, which broke collapse-by-clicking-the-box and mis-anchored by the
container padding. See [`group-expansion.md`](group-expansion.md), which removed
the header card and with it `groupAnchorIds`, `firstResolved` and
`HEADER_SUFFIX`.

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

If the id does not resolve after the reflow, the effect is a no-op and the
anchor clears: the viewport stays put rather than jumping somewhere arbitrary.
That branch has no test of its own — the helper whose miss case used to be
unit-tested was deleted with the successor rule (§4.3), and no path now reaches
it, since a card and a resource node both outlive their own reflow.

The pan is **relative** — it reads the current viewport and applies a delta — so
applying one anchor twice doubles the correction. StrictMode double-invokes mount
effects, so the effect guards on anchor identity (a ref holding the last applied
anchor) rather than trusting the dependency array to fire exactly once.

### 4.5 Anchored vs refit

`key={selectedId ?? "root"}` becomes `key={viewKey}`, a counter bumped **only**
for refit transitions, detected with the render-time pattern `GraphCanvas`
already uses for tooltip dismissal:

```ts
if (selectedId !== seenSelectedId) {
  setSeenSelectedId(selectedId);
  // Anchor only for the selection it was captured for
  if (!anchor || anchor.forSelection !== selectedId) {
    setViewKey((k) => k + 1);
    if (anchor) setAnchor(null);           // never carry a stale anchor into a refit
  }
  if (tip) setTip(null);
}
```

| Path | anchor | viewKey | Result |
| --- | --- | --- | --- |
| Canvas click, resource | set, matching | unchanged | no remount, anchored |
| Canvas click, group card | set | unchanged | `selectedId` never changes; anchored |
| Tree / details / k9s / `?focus=` | absent or mismatched | bumped | remount + `fitView`, as today |

Matching on `forSelection` rather than mere anchor *presence* matters because the
anchor lives from the click until the effect runs. The k9s handoff arrives as an
IPC callback rather than a discrete React event, so it can land inside that
window; presence alone would misread it as the click's own and suppress a refit
that should happen.

## 5. Knock-on: `RecenterButton`'s home viewport

`home` is currently recaptured on every selection change, because the remount
destroys the component. Once anchored transitions stop remounting, `home` can
outlive the subgraph it was measured against: `recenter()` calls `fitView()`,
lands on a viewport that does not match the stale `home`, and the button never
hides again.

Fix: `recenter` awaits `fitView` and *adopts* the landing viewport as the new
home, instead of comparing against the stale one. This regression is introduced
by removing the guaranteed remount, so it is in scope here.

Two things about `fitView` shape this, both verified against the installed
@xyflow/react 12.10.2 rather than assumed from its type:

- Its `Promise<boolean>` resolves only ever with `true` — one `resolve(true)`
  call in the whole bundle. The `false` branch that its *type* advertises belongs
  to the sibling helpers (`zoomIn`, `setCenter`, …), so a `!fitted` check is dead
  code.
- It settles when the transition **finishes**, and only once nodes are
  initialised. With an empty graph — reachable via `Ctrl-0` or the View menu
  before discovery has run — it never settles at all.

So `recenter` bails out when there are no nodes, and otherwise takes the settled
viewport as home. An earlier attempt used a `recentering` flag cleared from the
move-end handler; that left the flag stuck on an empty graph (no move-end ever
arrives) so the user's next pan was adopted as home, and it adopted a
half-animated viewport when two re-centers overlapped — d3-zoom dispatches `end`
on interrupt too. Deriving home from the settled promise removes both: concurrent
re-centers share one resolver, so they all see the same final viewport.

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
- `absolutePositions` for a nested container, a dangling `parentId`, top-level
  passthrough, and a parent cycle — including that a cycle resolves identically
  whichever member is visited first, since an order-dependent break point would
  pan by an arbitrary offset.

**Component** — `web/src/components/GraphCanvas.anchor.test.tsx` renders the real
`App` with the real canvas (only `api/client` is mocked) and reads screen
positions straight off the DOM transforms:

- a clicked resource holds its screen position **while its layout position
  changes** — both halves are asserted, because without the layout half the test
  is vacuous (see below);
- the canvas is *not* remounted for a canvas-originated selection, and *is* for a
  tree-originated one — asserted via DOM element identity, since a key change
  rebuilds every node element;
- a group card expands and collapses again, and collapses when the expanded
  **container background** is clicked (the §4.3 shared-id case);
- toggling a group card leaves the selection untouched (§6).

**Two traps this suite had to be built around**, both of which produced
confidently-passing tests that verified nothing:

1. *Fixture shape.* With a single namespace branch, `focusSubgraph` returns a
   byte-identical node and edge set before and after a deployment is selected —
   the spine re-adds the ancestors and descends to the same pods — so the layout
   never changes, the anchor delta is `(0,0)`, and a screen-position assertion
   holds even with anchoring deleted. The fixture therefore carries a **second
   namespace subtree** that selection prunes, and the test asserts the layout
   position genuinely moved.
2. *No observable for the remount.* `fitView` is a no-op under jsdom, so
   "did the view re-frame?" cannot distinguish anchored from refit — reverting
   `key={viewKey}` to `key={selectedId}` left every geometry assertion passing.
   DOM element identity is the observable that does discriminate, and it was
   verified to fail against the reverted implementation.

Use `fireEvent`, not `user-event`: a full pointer sequence reaches d3-zoom's
mousedown handler, which dereferences `event.view` — null on jsdom-dispatched
events — and crashes the test.

**`RecenterButton` is deliberately left untested**, despite §5 being where a bug
actually shipped. Its behaviour is only observable through a viewport transition
end, which comes from real d3-zoom gestures that jsdom can't produce; and the
specific empty-graph bug manifested as a *stuck flag*, which the fix removes
rather than corrects. A test asserting "the viewport doesn't move on an empty
graph" would pass against the broken and fixed versions alike — a third vacuous
test would be worse than a documented gap. Verifying this properly needs a real
browser.

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
| `web/src/lib/viewport.ts` | new — pure compensation math and absolute positions |
| `web/src/lib/viewport.test.ts` | new — unit tests for the above |
| `web/src/components/GraphCanvas.anchor.test.tsx` | new — DOM-level anchoring/toggle tests, with the jsdom stubs xyflow needs |
| `web/src/components/GraphCanvas.tsx` | anchor state, `AnchorKeeper` child inside `<ReactFlow>`, `viewKey` replacing the `selectedId` remount key, `RecenterButton` home adoption |
| `README.md` | the Quickstart's click behaviour, plus a jsdom/canvas-testing note under "notes that save you a debugging session" |

`App.tsx` is unchanged: origin detection lives entirely in `GraphCanvas`, which
already knows whether a click came from its own canvas.
