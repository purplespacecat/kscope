package graph

import (
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The projection is the agent-facing view of a snapshot: one resource's
// neighbourhood as indented text, bounded by depth and by bytes. It is a
// pure function — no files, no flags, no clock — so it is table-tested here
// and rendered by whichever front end wants it. See docs/agent-cli.md §4.

const (
	DefaultDepth  = 2
	MaxDepth      = 3
	DefaultBudget = 8192
	MinBudget     = 1024
)

var (
	ErrDepth    = errors.New("graph: depth must be between 1 and 3")
	ErrBudget   = errors.New("graph: budget must be at least 1024 bytes")
	ErrNoFocus  = errors.New("graph: focus node not in snapshot")
	ErrSkeleton = errors.New("graph: budget too small for the focus and its ancestors")
)

// ViewOptions bounds a projection. Zero values mean the defaults.
type ViewOptions struct {
	Depth   int // containment levels below the focus
	Budget  int // output bytes
	Reserve int // bytes the caller appends after Text; counted against Budget
}

// Rendered is the projection plus what the budget cost it.
type Rendered struct {
	Text         string
	OmittedNodes int
	OmittedEdges int
}

type rowKind int

const (
	rowNode    rowKind = iota // one resource
	rowGroup                  // "Pods (47)" header
	rowMember                 // a member shown under a group
	rowMore                   // "… +44 more"
	rowKubectl                // the focus's kubectl line, no connector
	rowEdge                   // Task 5
)

// row is one output line plus its children, built before anything is
// written so truncation can drop rows and connectors can be chosen with
// knowledge of what follows.
type row struct {
	kind   rowKind
	label  string // left text, without connector
	right  string // health / rollup text
	level  int    // containment levels below the focus; focus = 0
	health Health
	name   string // sort key within a level
	nodes  int    // snapshot nodes this row stands for (group: its total)
	bytes  int    // rendered length of this line alone, set by measure
	dead   bool   // removed by truncation; pruned before the single render
	kids   []*row
}

// groupAt mirrors GROUP_AT in web/src/components/GraphCanvas.tsx; the two are
// not shared, so keep them equal by hand.
const groupAt = 3

// groupExamples is how many members a group lists before "… +K more".
const groupExamples = 3

func severity(h Health) int {
	switch h {
	case HealthError:
		return 3
	case HealthWarning:
		return 2
	case HealthUnknown:
		return 1
	default:
		return 0
	}
}

// rollup summarises a group's health as "✓3 !1 ✗1", omitting zero counts so
// the common all-healthy case is just "✓47".
func rollup(nodes []Node) string {
	counts := map[Health]int{}
	for _, n := range nodes {
		counts[n.Health]++
	}
	var parts []string
	for _, h := range []Health{HealthHealthy, HealthWarning, HealthError, HealthUnknown} {
		if c := counts[h]; c > 0 {
			parts = append(parts, healthGlyph(h)+strconv.Itoa(c))
		}
	}
	return strings.Join(parts, " ")
}

// glyphCol is the rune column where health glyphs line up. Connectors are
// multi-byte, so alignment counts runes, not bytes.
const glyphCol = 44

const indentStep = "   "

func healthGlyph(h Health) string {
	switch h {
	case HealthHealthy:
		return "✓"
	case HealthWarning:
		return "!"
	case HealthError:
		return "✗"
	default:
		return "?"
	}
}

// HealthGlyph is the one-rune health marker the projection uses; exported so
// list output uses the same vocabulary.
func HealthGlyph(h Health) string { return healthGlyph(h) }

// nodeLabel is the only naming an agent sees: kind and name, never the ID.
func nodeLabel(n Node) string {
	if n.Kind == "Namespace" {
		return "ns " + n.Name
	}
	return n.Kind + " " + n.Name
}

// healthText is the right-hand column. A reason beats the health word: an
// agent reading "✗ CrashLoopBackOff" knows its next kubectl call.
func healthText(n Node, focus bool) string {
	g := healthGlyph(n.Health)
	switch {
	case n.Reason != "":
		return g + " " + n.Reason
	case focus:
		return g + " " + string(n.Health)
	default:
		return g
	}
}

type view struct {
	byID     map[string]Node
	children map[string][]Node
	outgoing map[string][]Edge // by Source
	incoming map[string][]Edge // by Target
	depth    int
	sb       strings.Builder
}

// Neighbourhood renders focusID's ancestors, its descendants to opts.Depth
// levels, and (Task 5) one hop of edges over that set.
func Neighbourhood(snap Snapshot, focusID string, opts ViewOptions) (Rendered, error) {
	depth := opts.Depth
	if depth == 0 {
		depth = DefaultDepth
	}
	if depth < 1 || depth > MaxDepth {
		return Rendered{}, ErrDepth
	}
	budget := opts.Budget
	if budget == 0 {
		budget = DefaultBudget
	}
	if budget < MinBudget {
		return Rendered{}, ErrBudget
	}
	if opts.Reserve < 0 || budget-opts.Reserve < MinBudget {
		return Rendered{}, ErrBudget
	}

	v := &view{byID: make(map[string]Node, len(snap.Nodes)), children: map[string][]Node{}, depth: depth}
	for _, n := range snap.Nodes {
		v.byID[n.ID] = n
	}
	focus, ok := v.byID[focusID]
	if !ok {
		return Rendered{}, ErrNoFocus
	}
	for _, n := range snap.Nodes {
		if n.ParentID != "" {
			v.children[n.ParentID] = append(v.children[n.ParentID], n)
		}
	}
	for _, cs := range v.children {
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].Kind != cs[j].Kind {
				return cs[i].Kind < cs[j].Kind
			}
			return cs[i].Name < cs[j].Name
		})
	}
	v.outgoing, v.incoming = map[string][]Edge{}, map[string][]Edge{}
	for _, e := range snap.Edges {
		v.outgoing[e.Source] = append(v.outgoing[e.Source], e)
		v.incoming[e.Target] = append(v.incoming[e.Target], e)
	}

	// Ancestors, root first. Always complete: orientation is not what
	// --depth trades away. Walking up appends and reverses once rather than
	// prepending per step, which copied the whole accumulated chain every
	// iteration; and a visited set stops a malformed ParentID cycle on the
	// second sighting of a node instead of after len(snap.Nodes) laps of it.
	// A ParentID naming a node outside the snapshot simply ends the walk.
	var chain []Node
	seen := map[string]bool{focus.ID: true}
	for cur, ok := v.byID[focus.ParentID]; ok && !seen[cur.ID]; cur, ok = v.byID[cur.ParentID] {
		seen[cur.ID] = true
		chain = append(chain, cur)
	}
	slices.Reverse(chain)
	indent := ""
	for i, a := range chain {
		connector := ""
		if i > 0 {
			connector = lastConnectorBytes
		}
		v.line(indent+connector, nodeLabel(a), healthText(a, false))
		if i > 0 {
			indent += indentStep
		}
	}
	connector := ""
	if len(chain) > 0 {
		connector = lastConnectorBytes
	}

	// The focus line carries the two most probable next actions: the Flux
	// object that manages it, and the kubectl call that fetches it.
	right := healthText(focus, true)
	if g := focus.GitOps; g != nil {
		right += "    [" + g.Tool + ": " + g.Kind + "/" + g.Name + "]"
	}
	v.line(indent+connector, nodeLabel(focus), right)
	childIndent := indent
	if len(chain) > 0 {
		childIndent += indentStep
	}
	var kids []*row
	if focus.Kubectl != "" {
		kids = append(kids, &row{kind: rowKubectl, label: focus.Kubectl, level: 0})
	}
	kids = append(kids, v.build(focus.ID, 1)...)
	kids = append(kids, v.edgeRows([]string{focus.ID}, 0, focus.Health)...)

	skeleton := v.sb.String() // ancestors + focus line, already written

	// Truncation removes rows in the total order of §4.3 — group members,
	// then edges, then nodes and groups — and needs to know, after each
	// removal, whether what is left fits. Re-rendering to find that out is
	// quadratic (every removal re-emits the whole surviving tree), and a
	// wide focus on an all-namespaces snapshot has thousands of rows. So
	// measure every row once, keep a running total, and render exactly once
	// at the end.
	measure(kids, len(childIndent))
	total := len(skeleton)
	for _, r := range kids {
		total += liveBytes(r)
	}

	// Each step's candidates are collected and ordered once rather than per
	// removal. That is the same sequence the repeated pick produced: the
	// steps run strictly one after another (a later step never puts a row
	// back into an earlier step's pool), removal never reorders the rows it
	// leaves behind, and step 3's deepest-first rule means a row's
	// descendants are always removed before the row itself.
	var groups, edges, nodes []*row
	collectRemovable(kids, &groups, &edges, &nodes)
	sortRemovals(groups)
	sortRemovals(edges)
	sortRemovals(nodes)

	omittedN, omittedE := 0, 0
	marker := ""
	gi, ei, ni := 0, 0, 0
	for total+len(marker)+opts.Reserve > budget {
		// 1. Empty one group's member and "more" rows, keeping its edges.
		//    Costs no omissions: the header still states the full count.
		if gi < len(groups) {
			g := groups[gi]
			gi++
			var kept []*row
			for _, k := range g.kids {
				if k.kind == rowEdge {
					kept = append(kept, k)
					continue
				}
				total -= liveBytes(k)
			}
			g.kids = kept
			continue
		}
		// 2. Drop one edge row.
		if ei < len(edges) {
			r := edges[ei]
			ei++
			if r.dead {
				continue
			}
			b, _, e := kill(r)
			total -= b
			omittedE += e
			marker = markerLine(childIndent, omittedN, omittedE)
			continue
		}
		// 3. Drop one node or group, and whatever still hangs off it.
		if ni < len(nodes) {
			r := nodes[ni]
			ni++
			if r.dead {
				continue
			}
			b, n, e := kill(r)
			total -= b
			omittedN += n
			omittedE += e
			marker = markerLine(childIndent, omittedN, omittedE)
			continue
		}
		// Nothing left but the skeleton (ancestors, focus, kubectl), and it
		// is reserved — it can never be truncated. It still does not fit, so
		// that is a real error, not silent oversize output.
		return Rendered{}, ErrSkeleton
	}

	v.sb.Reset()
	v.sb.WriteString(skeleton)
	prune(&kids)
	v.emit(kids, childIndent)
	v.sb.WriteString(marker)
	return Rendered{Text: v.sb.String(), OmittedNodes: omittedN, OmittedEdges: omittedE}, nil
}

// markerLine is the one line every omission is counted into.
func markerLine(indent string, nodes, edges int) string {
	return indent + "… truncated: " + strconv.Itoa(nodes) + " nodes, " + strconv.Itoa(edges) +
		" edges omitted\n"
}

// measure records what each row costs when emitted at the given indent,
// which is pure ASCII spaces so its byte and rune widths are equal. A row's
// length does not change as its siblings are removed: ├─ and └─ are the same
// width, and the padding before the right-hand column is computed from the
// row's own runes. That invariant is what lets the loop above keep a running
// total instead of re-rendering.
func measure(rows []*row, indent int) {
	for _, r := range rows {
		if r.kind == rowKubectl {
			r.bytes = indent + len(r.label) + 1 // no connector, then '\n'
			continue
		}
		r.bytes = indent + len(connectorBytes) + len(r.label) + 1
		if r.right != "" {
			pad := glyphCol - (indent + connectorRunes + utf8.RuneCountInString(r.label))
			if pad < 1 {
				pad = 1
			}
			r.bytes += pad + len(r.right)
		}
		measure(r.kids, indent+len(indentStep))
	}
}

// The two connectors emit uses. Both are one width, so which one a row gets
// cannot change what it costs — that is what lets measure keep a running
// total instead of re-rendering. emit reads these same constants so the
// widths cannot drift apart; connectorsAgree in the tests pins the equality.
const (
	connectorBytes     = "├─ "
	lastConnectorBytes = "└─ "
	connectorRunes     = 3
)

// liveBytes is what a row and everything still under it occupy.
func liveBytes(r *row) int {
	if r.dead {
		return 0
	}
	n := r.bytes
	for _, k := range r.kids {
		n += liveBytes(k)
	}
	return n
}

// kill marks a row and everything still live beneath it as removed, and
// returns the bytes that frees plus what it costs: the snapshot nodes and
// the edge rows it stood for, each counted exactly once.
func kill(r *row) (bytes, nodes, edges int) {
	if r.dead {
		return 0, 0, 0
	}
	r.dead = true
	bytes = r.bytes
	switch r.kind {
	case rowNode, rowGroup:
		nodes = r.nodes
	case rowEdge:
		edges = 1
	}
	for _, k := range r.kids {
		b, n, e := kill(k)
		bytes += b
		nodes += n
		edges += e
	}
	return bytes, nodes, edges
}

// prune drops the killed rows, so emit sees only survivors and picks the
// last-child connector from them.
func prune(rows *[]*row) {
	kept := (*rows)[:0]
	for _, r := range *rows {
		if r.dead {
			continue
		}
		prune(&r.kids)
		kept = append(kept, r)
	}
	*rows = kept
}

// collectRemovable gathers each step's candidates in one pre-order walk:
// groups that still list members, every edge row, and every node and group.
// Group members and "more" rows are never candidates on their own — step 1
// removes them as a unit via their group.
func collectRemovable(rows []*row, groups, edges, nodes *[]*row) {
	for _, r := range rows {
		switch r.kind {
		case rowGroup:
			for _, k := range r.kids {
				if k.kind == rowMember || k.kind == rowMore {
					*groups = append(*groups, r)
					break
				}
			}
			*nodes = append(*nodes, r)
		case rowNode:
			*nodes = append(*nodes, r)
		case rowEdge:
			*edges = append(*edges, r)
		}
		if r.kind == rowNode || r.kind == rowGroup {
			collectRemovable(r.kids, groups, edges, nodes)
		}
	}
}

// sortRemovals orders candidates deepest-level first, then healthy before
// unhealthy, then by label, then by name — the total order of §4.3. The sort
// is stable, so rows that tie on all four keys are removed in tree order.
func sortRemovals(rows []*row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.level != b.level {
			return a.level > b.level
		}
		if sa, sb := severity(a.health), severity(b.health); sa != sb {
			return sa < sb
		}
		if a.label != b.label {
			return a.label < b.label
		}
		return a.name < b.name
	})
}

// build returns the rows for id's children at the given level, grouping
// leaf siblings of one kind once there are groupAt of them. Namespaces are
// the drill path and never group.
func (v *view) build(id string, level int) []*row {
	if level > v.depth {
		return nil
	}
	kids := v.children[id]
	byKind := map[string][]Node{}
	var kindOrder []string
	for _, k := range kids {
		if _, seen := byKind[k.Kind]; !seen {
			kindOrder = append(kindOrder, k.Kind)
		}
		byKind[k.Kind] = append(byKind[k.Kind], k)
	}

	var rows []*row
	for _, kind := range kindOrder {
		members := byKind[kind]
		allLeaves := true
		for _, m := range members {
			if len(v.children[m.ID]) > 0 {
				allLeaves = false
				break
			}
		}
		if len(members) >= groupAt && allLeaves && kind != "Namespace" {
			rows = append(rows, v.groupRow(kind, members, level))
			continue
		}
		for _, k := range members {
			r := &row{kind: rowNode, label: nodeLabel(k), right: healthText(k, false), level: level, health: k.Health, name: k.Name, nodes: 1}
			r.kids = v.build(k.ID, level+1)
			r.kids = append(r.kids, v.edgeRows([]string{k.ID}, level, k.Health)...)
			rows = append(rows, r)
		}
	}
	return rows
}

func (v *view) groupRow(kind string, members []Node, level int) *row {
	sorted := append([]Node(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if si, sj := severity(sorted[i].Health), severity(sorted[j].Health); si != sj {
			return si > sj
		}
		return sorted[i].Name < sorted[j].Name
	})
	worst := HealthHealthy
	for _, m := range members {
		if severity(m.Health) > severity(worst) {
			worst = m.Health
		}
	}
	g := &row{
		kind:   rowGroup,
		label:  pluralize(kind) + " (" + strconv.Itoa(len(members)) + ")",
		right:  rollup(members),
		level:  level,
		health: worst,
		name:   kind,
		nodes:  len(members),
	}
	shown := sorted
	if len(shown) > groupExamples {
		shown = shown[:groupExamples]
	}
	for _, m := range shown {
		g.kids = append(g.kids, &row{kind: rowMember, label: m.Name, right: healthText(m, false), level: level, health: m.Health, name: m.Name, nodes: 1})
	}
	if rest := len(members) - len(shown); rest > 0 {
		g.kids = append(g.kids, &row{kind: rowMore, label: "… +" + strconv.Itoa(rest) + " more", level: level, nodes: rest})
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.ID
	}
	g.kids = append(g.kids, v.edgeRows(ids, level, worst)...)
	return g
}

// edgeRows renders one hop of wiring for the given owners, deduplicated on
// (direction, kind, peer) so a group of forty-seven pods mounting one Secret
// is one line. Peers are leaves: nothing is walked from them. Edges whose
// peer is missing from the snapshot are skipped rather than named.
func (v *view) edgeRows(owners []string, level int, ownerHealth Health) []*row {
	seen := map[string]bool{}
	var rows []*row
	for _, id := range owners {
		for _, e := range v.outgoing[id] {
			peer, ok := v.byID[e.Target]
			if !ok || seen["out|"+e.Kind+"|"+e.Target] {
				continue
			}
			seen["out|"+e.Kind+"|"+e.Target] = true
			label := e.Kind + " → " + nodeLabel(peer)
			rows = append(rows, &row{kind: rowEdge, label: label, level: level, health: ownerHealth, name: "0" + label})
		}
		for _, e := range v.incoming[id] {
			peer, ok := v.byID[e.Source]
			if !ok || seen["in|"+e.Kind+"|"+e.Source] {
				continue
			}
			seen["in|"+e.Kind+"|"+e.Source] = true
			label := "← " + e.Kind + " " + nodeLabel(peer)
			rows = append(rows, &row{kind: rowEdge, label: label, level: level, health: ownerHealth, name: "1" + label})
		}
	}
	// name carries a direction prefix so one sort puts outgoing first.
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

// emit writes rows at indent, choosing ├─/└─ by position. Kubectl rows are
// bare: they hang under the focus without a connector.
func (v *view) emit(rows []*row, indent string) {
	for i, r := range rows {
		if r.kind == rowKubectl {
			v.sb.WriteString(indent + r.label + "\n")
			continue
		}
		connector := connectorBytes
		if i == len(rows)-1 {
			connector = lastConnectorBytes
		}
		v.line(indent+connector, r.label, r.right)
		v.emit(r.kids, indent+indentStep)
	}
}

// line writes prefix+label, pads to the glyph column, then the right side.
func (v *view) line(prefix, label, right string) {
	left := prefix + label
	v.sb.WriteString(left)
	if right != "" {
		pad := glyphCol - utf8.RuneCountInString(left)
		if pad < 1 {
			pad = 1
		}
		v.sb.WriteString(strings.Repeat(" ", pad))
		v.sb.WriteString(right)
	}
	v.sb.WriteByte('\n')
}
