# Group expansion — members below, card stays put

Status: **implemented** on `fix/group-expand-below`. Placement is covered by
component tests in both packing cases; the anti-throw half is browser-only
(see §7).

## 1. Problem

Expanding a kind-group throws the view apart. Observed on a `flux-system` focus
with `GitRepositories` expanded: the namespace's children end up in **two boxes
side by side** — a grid on the left holding `flux`, Services, NetworkPolicies and
ServiceAccounts, and a second box on the right holding the expanded group — with
content clipped off **both** edges of the viewport.

The cause is in `layout()`. When a group is expanded it is **removed** from the
parent's `mixed` set and given its own container, and both that container and the
residual `__grid__` container get `g.setEdge(parent, …)`. They are therefore
**siblings at the same dagre rank**, so dagre places them beside each other. The
expanded box is `members + 1` slots wide — six members is ~1300px — so the pair
overflows the viewport and the grid is pushed off to one side.

Anchoring (see [`focus-anchoring.md`](focus-anchoring.md)) holds the clicked card's
pixel, but everything around it still moves, and the hierarchy stops reading as
one tree. Two boxes at the same rank look like two unrelated trees.

## 2. Intent

Expanding a group must not move anything that was already on screen. Members
appear **below**, in the direction the tree already grows, and the group card
keeps its slot.

## 3. Mechanism

### 3.1 The card stays in the grid

The group card stays in the parent's `mixed` set in **both** states, so its slot
and the grid's size are unchanged by a toggle. The `Slot` `g` variant carries
`expanded`, which `groupHeaderCard` already took as a parameter — the grid branch
used to hardcode it to `false`.

### 3.2 A members box, ranked below

A separate container `${gid}__m` holds only the members. It has no header slot:
the card in the grid *is* the header. It carries `groupToggle`, so clicking its
background still collapses the group.

Ranking hangs it off whichever dagre node stands in for the card:

| Parent's children | Card is | dagre edge |
| --- | --- | --- |
| more than `WRAP_AT` | packed inside `__grid__<parent>` | `__grid__<parent>` → `${gid}__m` |
| `WRAP_AT` or fewer | an ordinary dagre node | `gid` → `${gid}__m` |

Which case applies is only known after `mixed` is built, so members boxes are
registered after the grid decision rather than inside the bucket loop.

Bookkeeping that has to follow:

- members stay in `packed` either way — they are laid out by their box, not by
  dagre directly;
- `memberOf` maps each member to `${gid}__m`, which is what keeps relationship
  edges touching packed members dimmed;
- `hiddenMembers` still applies to collapsed groups only, unchanged;
- `Container.header` is replaced by an optional `toggle` holding the `gid`, since
  a container no longer renders a header card but still needs to be clickable.
  `gridSize(members.length)` loses its `+1` header slot.

### 3.3 Rendered edge ≠ dagre edge

The **drawn** edge always runs card → box, even where dagre's edge ran
grid → box. Dagre needs the container for ranking; the user needs a line from the
card they clicked to the members it revealed. The same split already exists for
infra containment, where the rendered edge is flipped relative to the layout edge.

## 4. Consequence: the successor machinery dies

The card's id is `gid` in both states — it never changes identity. So:

- the anchor is simply "hold `gid` where it is";
- `groupAnchorIds`, `firstResolved` and `HEADER_SUFFIX` become unreachable and are
  deleted, along with the `${gid}__h` header card;
- the anchor's `pos` is taken from the **card's** position rather than the clicked
  node's, so collapsing by clicking the box background anchors on the card too
  instead of comparing two different points.

This also removes a whole class of bug for good: the expanded container used to
share the collapsed card's id, which is what made a background click read as
"expand" and broke collapse-by-clicking-the-box.

## 5. Limits, stated plainly

- **dagre may still recentre a rank.** A new child below the grid can shift the
  grid horizontally so it centres over its children. Anchoring is what makes that
  invisible: the pan follows the card. "Nothing moves" is true of the card, not of
  every pixel.
- **Two groups expanded at once** put two boxes at the same rank, side by side, so
  width grows again. Stacking them would imply a parent-child relationship between
  unrelated groups, which reads worse than the width. Not solved here.
- The members box is still up to `WRAP_AT` (6) columns, ~1300px. The motivating
  case — four `GitRepositories` — is ~880px and fits.
- **When the card is packed into a grid, the box centres under the *grid*, not
  under its own card**, because the grid is what it hangs off in dagre. Measured
  on a seven-kind namespace: grid spans x 239–1551, a lone 880px box centres at
  x≈895, while a card in column 0 sits at x≈255 — so the rendered card → box edge
  travels ~640px sideways across the grid's footprint. The box is still below
  everything, and nothing is pushed off-screen, but "below the card" is precise
  only in the ungridded case.

## 6. Non-goals

- No change to which children group (`GROUP_AT`, `GROUP_EXEMPT`), to the node
  budget, or to `focusSubgraph`.
- No change to the infra flip, which inverts *containment* direction for
  Nodes/ControlPlane/Components. That is a separate concern from group expansion.
- No virtualisation or column-count tuning.

## 7. Testing

Container **members** get meaningless positions under jsdom — xyflow clamps every
`extent: "parent"` child against a parent it measures as 0×0 (recorded in
[`focus-anchoring.md`](focus-anchoring.md) §7). A fixture with `WRAP_AT` or fewer
mixed slots avoids that entirely: the card is then an ordinary dagre node with a
real position, which is exactly the node under test.

- the members box appears **below** the card, clearing it by more than a card's
  height — this is the assertion that fails against the old sibling-box layout;
- expanding and collapsing both work from the card, which is present throughout;
- collapsing from the **box background** works, and leaves the card in place;
- with a parent past `WRAP_AT` — seven group-forming kinds, the shape from the
  original report — the box clears the **grid's bottom edge**, which is what
  distinguishes a rank below from a sibling on the same rank. Comparing bare `y`
  values passes either way, because dagre centres nodes within a rank and a
  shorter box sits a few pixels lower than a taller grid even as its sibling; the
  first version of this test was vacuous for exactly that reason.

**Two things measurement corrected during implementation**, both of which this
document originally over-promised:

1. *The card's layout position is not identical.* Expanding widens the subtree, so
   dagre recentres and the card's layout `x` moves (measured: 230 → 556 on the test
   fixture). §5 anticipated this; the test does not assert otherwise.
2. *The card's screen position cannot be asserted in jsdom.* Expanding adds nodes,
   which triggers the initial `fitView` that never completed for want of
   measurement — it lands after the anchor pan and permanently overwrites it
   (measured: zoom goes 1 → 0.07 on the expand, which anchoring never does). So
   the "don't throw me around" half is **browser-verified only**, exactly as it is
   for container geometry.

Also not covered: the multi-box side-by-side case, and the horizontal placement
of a box relative to its card (only its vertical rank is asserted).

## 8. Files touched

| File | Change |
| --- | --- |
| `web/src/components/GraphCanvas.tsx` | `layout()`: card stays in `mixed` with an `expanded` flag; members box `${gid}__m` ranked below; rendered edge card → box; header card removed; anchor takes the card's position |
| `web/src/lib/viewport.ts` | `groupAnchorIds`, `firstResolved`, `HEADER_SUFFIX` deleted |
| `web/src/lib/viewport.test.ts` | tests for the deleted helpers removed |
| `web/src/components/GraphCanvas.anchor.test.tsx` | expansion assertions updated to the card-stays-put model; new placement tests |
| `docs/focus-anchoring.md` | §4.3 rewritten: no successor rule, the card's id is stable |
