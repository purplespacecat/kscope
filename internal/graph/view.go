package graph

import (
	"errors"
	"sort"
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
	if focus.Kubectl != "" {
		v.sb.WriteString(childIndent + focus.Kubectl + "\n")
	}

	v.descend(focus.ID, childIndent, 1)

	return Rendered{Text: v.sb.String()}, nil
}

// descend renders the children of id at the given indent, recursing while
// the level is within depth.
func (v *view) descend(id, indent string, level int) {
	if level > v.depth {
		return
	}
	kids := v.children[id]
	for i, k := range kids {
		connector := "├─ "
		if i == len(kids)-1 {
			connector = "└─ "
		}
		v.line(indent+connector, nodeLabel(k), healthText(k, false))
		v.descend(k.ID, indent+indentStep, level+1)
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
