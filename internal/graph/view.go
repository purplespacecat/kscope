package graph

import (
	"errors"
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
	ErrDepth   = errors.New("graph: depth must be between 1 and 3")
	ErrBudget  = errors.New("graph: budget must be at least 1024 bytes")
	ErrNoFocus = errors.New("graph: focus node not in snapshot")
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
	// --depth trades away. The length guard makes a malformed parent cycle
	// terminate instead of spinning.
	var chain []Node
	for cur, ok := v.byID[focus.ParentID]; ok && len(chain) <= len(snap.Nodes); cur, ok = v.byID[cur.ParentID] {
		chain = append([]Node{cur}, chain...)
	}
	indent := ""
	for i, a := range chain {
		connector := ""
		if i > 0 {
			connector = "└─ "
		}
		v.line(indent+connector, nodeLabel(a), healthText(a, false))
		if i > 0 {
			indent += indentStep
		}
	}
	connector := ""
	if len(chain) > 0 {
		connector = "└─ "
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
	omittedN, omittedE := 0, 0
	for {
		v.sb.Reset()
		v.sb.WriteString(skeleton)
		v.emit(kids, childIndent)
		if omittedN > 0 || omittedE > 0 {
			v.sb.WriteString(childIndent + "… truncated: " + strconv.Itoa(omittedN) + " nodes, " + strconv.Itoa(omittedE) + " edges omitted\n")
		}
		if v.sb.Len()+opts.Reserve <= budget {
			break
		}
		if shrinkGroup(&kids) {
			continue
		}
		if _, e, ok := dropOne(&kids, func(r *row) bool { return r.kind == rowEdge }); ok {
			omittedE += e
			continue
		}
		n, e, ok := dropOne(&kids, func(r *row) bool { return r.kind == rowNode || r.kind == rowGroup })
		if !ok {
			break // skeleton only; MinBudget guarantees it fits
		}
		omittedN += n
		omittedE += e
	}
	return Rendered{Text: v.sb.String(), OmittedNodes: omittedN, OmittedEdges: omittedE}, nil
}

// subtreeCounts returns the snapshot nodes and edge rows a row stands for,
// itself included, so a dropped subtree is reported exactly once.
func subtreeCounts(r *row) (nodes, edges int) {
	switch r.kind {
	case rowNode, rowGroup:
		nodes += r.nodes
	case rowEdge:
		edges++
	}
	for _, k := range r.kids {
		n, e := subtreeCounts(k)
		nodes += n
		edges += e
	}
	return nodes, edges
}

// candidate is a removable row and where it lives, so removal is a slice
// edit on its parent.
type candidate struct {
	parent *[]*row
	idx    int
	row    *row
}

// collect walks the tree for rows of the given kinds. Group members and
// "more" rows are never candidates on their own — step 1 removes them as a
// unit via their group.
func collect(rows *[]*row, want func(*row) bool, out *[]candidate) {
	for i, r := range *rows {
		if want(r) {
			*out = append(*out, candidate{parent: rows, idx: i, row: r})
		}
		if r.kind == rowNode || r.kind == rowGroup {
			collect(&r.kids, want, out)
		}
	}
}

// pickFirst orders candidates deepest-level first, then healthy before
// unhealthy, then by label, then by name — the total order of §4.3.
func pickFirst(cs []candidate) *candidate {
	if len(cs) == 0 {
		return nil
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i].row, cs[j].row
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
	return &cs[0]
}

func remove(c *candidate) {
	p := *c.parent
	*c.parent = append(p[:c.idx:c.idx], p[c.idx+1:]...)
}

// shrinkGroup empties one group's member and "more" rows, keeping its edge
// rows. Returns false when no group has anything left to shrink.
func shrinkGroup(kids *[]*row) bool {
	var cs []candidate
	collect(kids, func(r *row) bool {
		if r.kind != rowGroup {
			return false
		}
		for _, k := range r.kids {
			if k.kind == rowMember || k.kind == rowMore {
				return true
			}
		}
		return false
	}, &cs)
	c := pickFirst(cs)
	if c == nil {
		return false
	}
	var kept []*row
	for _, k := range c.row.kids {
		if k.kind == rowEdge {
			kept = append(kept, k)
		}
	}
	c.row.kids = kept
	return true
}

// dropOne removes the first candidate of the given kinds and returns what
// it cost.
func dropOne(kids *[]*row, want func(*row) bool) (nodes, edges int, ok bool) {
	var cs []candidate
	collect(kids, want, &cs)
	c := pickFirst(cs)
	if c == nil {
		return 0, 0, false
	}
	nodes, edges = subtreeCounts(c.row)
	remove(c)
	return nodes, edges, true
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
		connector := "├─ "
		if i == len(rows)-1 {
			connector = "└─ "
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
