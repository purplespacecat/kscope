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
	Depth  int // containment levels below the focus
	Budget int // output bytes
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
	v.emit(kids, childIndent)

	return Rendered{Text: v.sb.String()}, nil
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
	return g
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
