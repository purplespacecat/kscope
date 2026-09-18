import type { GraphEdge, GraphNode } from "../types/graph";
import { EDGE_STYLE, edgePhrase, INFRA_KINDS } from "./display";

/** A parent's leaf children of one kind fold once there are at least this many. */
const GROUP_AT = 3;

/** Namespaces are the primary drill path — never hide them behind a group. */
const GROUP_EXEMPT = new Set(["Namespace"]);

/**
 * Expansion state for the graph canvas. Every field is written only by a user
 * gesture — a toggle, a reveal, "show 1 parent", or the one-time seed at mount.
 * Nothing here is derived from selection, which is what stops the view
 * collapsing things the user did not collapse.
 */
export interface TreeState {
  /** Topmost visible card. */
  rootId: string | null;
  /** Nodes whose children are shown. */
  expanded: Set<string>;
  /** Kind-group cards whose members are shown. */
  expandedGroups: Set<string>;
}

/** A fold of same-kind leaf siblings, standing in for its members while collapsed. */
export interface GroupCard {
  id: string;
  kind: string;
  parentId: string;
  memberIds: string[];
  expanded: boolean;
}

export interface Visible {
  /** Real nodes on screen. */
  nodes: GraphNode[];
  /** Group cards on screen. */
  groups: GroupCard[];
  /** Containment among visible ids, group cards included. */
  containment: GraphEdge[];
}

/** Children of each node, in snapshot order. */
function childIndex(all: GraphNode[]): Map<string, GraphNode[]> {
  const byId = new Set(all.map((x) => x.id));
  const kids = new Map<string, GraphNode[]>();
  for (const x of all) {
    if (!x.parentId || !byId.has(x.parentId)) continue;
    const list = kids.get(x.parentId);
    if (list) list.push(x);
    else kids.set(x.parentId, [x]);
  }
  return kids;
}

/** Stable id for a parent's fold of one kind. */
export function groupId(parentId: string, kind: string): string {
  return `__kg__${parentId}__${kind}`;
}

const arc = (source: string, target: string): GraphEdge => ({
  id: `${source}->${target}`,
  source,
  target,
  kind: "contains",
});

export function visibleTree(all: GraphNode[], state: TreeState): Visible {
  const out: Visible = { nodes: [], groups: [], containment: [] };
  if (all.length === 0) return out;
  const byId = new Map(all.map((x) => [x.id, x]));
  const root = state.rootId ? byId.get(state.rootId) : undefined;
  if (!root) return out;

  const kids = childIndex(all);

  const walk = (node: GraphNode) => {
    out.nodes.push(node);
    if (!state.expanded.has(node.id)) return;

    const children = kids.get(node.id) ?? [];
    // Only childless, non-infra children can fold: a node with descendants is a
    // drill path, and folding it would hide a branch behind a count.
    const foldable = new Map<string, GraphNode[]>();
    const plain: GraphNode[] = [];
    for (const c of children) {
      const isLeaf = (kids.get(c.id) ?? []).length === 0;
      if (!isLeaf || INFRA_KINDS.has(c.kind) || GROUP_EXEMPT.has(c.kind)) {
        plain.push(c);
        continue;
      }
      const list = foldable.get(c.kind);
      if (list) list.push(c);
      else foldable.set(c.kind, [c]);
    }

    for (const [kind, members] of foldable) {
      if (members.length < GROUP_AT) {
        plain.push(...members);
        foldable.delete(kind);
      }
    }

    for (const c of plain) {
      out.containment.push(arc(node.id, c.id));
      walk(c);
    }

    for (const [kind, members] of foldable) {
      members.sort((a, b) => a.name.localeCompare(b.name));
      const gid = groupId(node.id, kind);
      const expanded = state.expandedGroups.has(gid);
      out.groups.push({
        id: gid,
        kind,
        parentId: node.id,
        memberIds: members.map((m) => m.id),
        expanded,
      });
      out.containment.push(arc(node.id, gid));
      if (!expanded) continue;
      for (const m of members) {
        out.containment.push(arc(gid, m.id));
        out.nodes.push(m);
      }
    }
  };

  walk(root);
  return out;
}

/** Ancestor ids of `id`, nearest first, using the given node set. */
function ancestry(byId: Map<string, GraphNode>, id: string): string[] {
  const out: string[] = [];
  let cur = byId.get(id)?.parentId;
  while (cur && byId.has(cur)) {
    out.push(cur);
    cur = byId.get(cur)?.parentId;
  }
  return out;
}

/**
 * Expand or collapse one node.
 *
 * Collapsing removes only that node, so its sub-branch is remembered and comes
 * back intact on re-expand. With `solo` on, expanding additionally collapses the
 * node's containment siblings — the toggle's whole purpose is keeping one rank
 * narrow. Siblings are collapsed, not pruned: a later re-expand restores them
 * the same way any other collapse does, so the gesture means one thing whether
 * or not solo is on. Group cards are left alone (see spec §2.3) — peers in a
 * rank, not competing branches.
 */
export function toggleExpand(
  all: GraphNode[],
  state: TreeState,
  id: string,
  solo: boolean,
): TreeState {
  const expanded = new Set(state.expanded);
  if (expanded.has(id)) {
    expanded.delete(id);
    return { ...state, expanded };
  }
  expanded.add(id);
  if (solo) {
    const byId = new Map(all.map((x) => [x.id, x]));
    const parentId = byId.get(id)?.parentId;
    if (parentId) {
      for (const x of all) {
        if (x.parentId === parentId && x.id !== id) expanded.delete(x.id);
      }
    }
  }
  return { ...state, expanded };
}

/** Show the group card's members, or hide them again. */
export function toggleGroup(state: TreeState, gid: string): TreeState {
  const expandedGroups = new Set(state.expandedGroups);
  if (!expandedGroups.delete(gid)) expandedGroups.add(gid);
  return { ...state, expandedGroups };
}

/**
 * Raise the visible root one level. The new root is expanded so the branch the
 * user arrived on stays on screen: gaining context must not cost them their place.
 */
export function showParent(all: GraphNode[], state: TreeState): TreeState {
  const byId = new Map(all.map((x) => [x.id, x]));
  const parentId = state.rootId ? byId.get(state.rootId)?.parentId : undefined;
  if (!parentId || !byId.has(parentId)) return state;
  const expanded = new Set(state.expanded);
  expanded.add(parentId);
  return { ...state, rootId: parentId, expanded };
}

/** The group card that would hold `node`, or undefined if it stands alone. */
function foldOf(all: GraphNode[], node: GraphNode): string | undefined {
  if (!node.parentId) return undefined;
  if (INFRA_KINDS.has(node.kind) || GROUP_EXEMPT.has(node.kind)) return undefined;
  const hasKids = new Set(all.map((x) => x.parentId).filter(Boolean) as string[]);
  if (hasKids.has(node.id)) return undefined;
  const sameKind = all.filter(
    (x) => x.parentId === node.parentId && x.kind === node.kind && !hasKids.has(x.id),
  );
  return sameKind.length >= GROUP_AT ? groupId(node.parentId, node.kind) : undefined;
}

/**
 * Bring a node on screen: expand every ancestor down to it, open the group card
 * holding it, and raise the root if the node sits outside the current root's
 * subtree. Writes to `expanded` — legitimate, because a reveal is an explicit
 * gesture (picking from the sidebar tree, arriving via ?focus=), not a side
 * effect of selection.
 */
export function reveal(
  all: GraphNode[],
  state: TreeState,
  id: string,
  solo: boolean,
): TreeState {
  const byId = new Map(all.map((x) => [x.id, x]));
  const target = byId.get(id);
  if (!target) return state;

  const chain = ancestry(byId, id);
  const expanded = new Set(state.expanded);
  const onPath = new Set([id, ...chain]);
  for (const a of chain) {
    expanded.add(a);
    // Solo narrows the revealed path the same way it narrows a manual expand:
    // anything beside the path folds away.
    if (!solo) continue;
    for (const x of all) {
      if (x.parentId === a && !onPath.has(x.id)) expanded.delete(x.id);
    }
  }
  let next: TreeState = { ...state, expanded };

  const gid = foldOf(all, target);
  if (gid) {
    const expandedGroups = new Set(next.expandedGroups);
    expandedGroups.add(gid);
    next = { ...next, expandedGroups };
  }

  // Outside the current root's subtree? Raise to the nearest common ancestor.
  const rootChain = next.rootId ? [next.rootId, ...ancestry(byId, next.rootId)] : [];
  const inSubtree = id === next.rootId || chain.includes(next.rootId ?? "");
  if (!inSubtree) {
    const targetChain = [id, ...chain];
    const common = rootChain.find((a) => targetChain.includes(a));
    next = { ...next, rootId: common ?? rootChain[rootChain.length - 1] ?? next.rootId };
  }
  return next;
}

/**
 * Carry expansion across a new snapshot. Re-running discovery over one scope
 * regenerates most ids identically — Pod names churn, structure mostly does not —
 * so dropping the open tree would cost the user their place for nothing. Dead ids
 * are dropped and a dead root falls back to its nearest surviving ancestor, which
 * needs the *previous* node set to walk.
 */
export function prune(
  prev: GraphNode[],
  next: GraphNode[],
  state: TreeState,
  selectedId: string | null,
): { state: TreeState; selectedId: string | null } {
  const alive = new Set(next.map((x) => x.id));
  const expanded = new Set([...state.expanded].filter((id) => alive.has(id)));

  const liveGroups = new Set<string>();
  for (const x of next) if (x.parentId) liveGroups.add(groupId(x.parentId, x.kind));
  const expandedGroups = new Set(
    [...state.expandedGroups].filter((gid) => liveGroups.has(gid)),
  );

  let rootId = state.rootId;
  if (!rootId || !alive.has(rootId)) {
    const prevById = new Map(prev.map((x) => [x.id, x]));
    const fallback = rootId
      ? ancestry(prevById, rootId).find((a) => alive.has(a))
      : undefined;
    rootId = fallback ?? next.find((x) => !x.parentId)?.id ?? next[0]?.id ?? null;
  }

  return {
    state: { rootId, expanded, expandedGroups },
    selectedId: selectedId && alive.has(selectedId) ? selectedId : null,
  };
}

/**
 * Edge kinds that are a property of the node rather than wiring between nodes.
 * "Managed by Flux" and "an instance of this CRD" are already on the card — as
 * the controller mark and as its Kind — so drawing them again is a line that
 * tells the reader nothing they cannot see.
 */
const MARK_KINDS = new Set(["managed-by", "instance-of"]);

/** nodeId → the Kind of whatever manages it, for a card mark. */
export function controllerMarks(
  all: GraphNode[],
  edges: GraphEdge[],
): Map<string, string> {
  const byId = new Map(all.map((x) => [x.id, x]));
  const marks = new Map<string, string>();
  for (const e of edges) {
    if (e.kind !== "managed-by") continue;
    const owner = byId.get(e.target);
    if (owner) marks.set(e.source, owner.kind);
  }
  return marks;
}

/**
 * Resolve any node id to the visible card standing in for it: itself if on
 * screen, else the group card holding it, else the nearest visible ancestor.
 */
function standInResolver(
  byId: Map<string, GraphNode>,
  visible: Visible,
): (id: string) => string | undefined {
  const onScreen = new Set(visible.nodes.map((x) => x.id));
  const holder = new Map<string, string>();
  for (const g of visible.groups) {
    if (g.expanded) continue;
    for (const m of g.memberIds) holder.set(m, g.id);
  }
  return (id: string) => {
    if (onScreen.has(id)) return id;
    const g = holder.get(id);
    if (g) return g;
    for (let cur = byId.get(id)?.parentId; cur; cur = byId.get(cur)?.parentId) {
      if (onScreen.has(cur)) return cur;
      const cg = holder.get(cur);
      if (cg) return cg;
    }
    return undefined;
  };
}

/**
 * One kind of relationship the selected card takes part in, ready to render as
 * a legend line and an outline colour.
 */
export interface Relation {
  /** Model edge kind, e.g. "selects". */
  kind: string;
  /** Plain English, from the selection's point of view. */
  phrase: string;
  /** Outline colour for the cards on the other end. */
  color: string;
  /** Visible cards to outline — group cards where a member is folded away. */
  cardIds: string[];
  /** How many underlying edges this line stands for. */
  count: number;
}

/**
 * What the selected card is related to.
 *
 * Arrows across a canvas turned out to be a poor way to say "these two things
 * are connected": they cross, they converge, and the reader still has to trace
 * a line to find the other end. This returns the same information as a set of
 * cards to outline plus a legend naming each colour, so the eye jumps straight
 * to the related cards wherever they sit in the tree.
 *
 * Hidden endpoints resolve to whatever card stands in for them, so a folded
 * group outlines as a whole rather than silently dropping the relationship.
 */
export function relate(
  all: GraphNode[],
  edges: GraphEdge[],
  visible: Visible,
  selectedId: string | null,
): Relation[] {
  if (!selectedId) return [];
  const byId = new Map(all.map((x) => [x.id, x]));
  const standIn = standInResolver(byId, visible);

  // Keyed by kind+direction: "mounts" out and "mounted by" in are two different
  // statements about the selection and deserve their own legend lines.
  const out = new Map<string, Relation>();
  for (const e of edges) {
    if (MARK_KINDS.has(e.kind)) continue;
    const outgoing = e.source === selectedId;
    const incoming = e.target === selectedId;
    if (!outgoing && !incoming) continue;

    const otherId = outgoing ? e.target : e.source;
    const card = standIn(otherId);
    // No stand-in means the other end is outside the visible root's subtree
    // entirely; nothing to outline, so there is nothing to say.
    if (!card || card === selectedId) continue;

    const key = `${e.kind}|${outgoing ? "out" : "in"}`;
    let rel = out.get(key);
    if (!rel) {
      rel = {
        kind: e.kind,
        phrase: edgePhrase(
          e.kind,
          outgoing ? "out" : "in",
          byId.get(e.source)?.kind,
        ),
        color: EDGE_STYLE[e.kind]?.stroke ?? "#64748b",
        cardIds: [],
        count: 0,
      };
      out.set(key, rel);
    }
    rel.count++;
    if (!rel.cardIds.includes(card)) rel.cardIds.push(card);
  }
  return [...out.values()];
}

/**
 * Card id → outline colour. A card taking part in two relationships keeps the
 * first one's colour: two rings on one card reads as a rendering fault rather
 * than as extra information, and the legend still lists both.
 */
export function outlineColors(relations: Relation[]): Map<string, string> {
  const colors = new Map<string, string>();
  for (const r of relations) {
    for (const id of r.cardIds) if (!colors.has(id)) colors.set(id, r.color);
  }
  return colors;
}
