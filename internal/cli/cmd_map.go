package cli

import (
	"errors"
	"fmt"

	"github.com/purplespacecat/kscope/internal/graph"
)

const mapSynopsis = `kscope map <name> [--namespace NS] [--kind K] [--depth N] [--budget BYTES] [--data-dir DIR]

Print a compact map of one resource: its place in the containment tree, its
descendants to --depth levels (default 2, max 3), one hop of relationships in
both directions, and health with reasons. Output never exceeds --budget bytes
(default 8192; 1024 is the documented minimum for the map projection itself —
the footer and counts line are reserved on top, so the smallest --budget this
snapshot will accept is somewhat higher, and the command names it if you go
below it). Flags may come before or after the name.

Exit 0: printed. Exit 2: no match or ambiguous — stderr names the fix.`

func init() {
	register(command{name: "map", summary: "print one resource's neighbourhood: containment, one hop of wiring, health", synopsis: mapSynopsis, run: runMap})
}

func runMap(args []string, io IO) int {
	c := newFlags("map", mapSynopsis, io)
	ns := c.fs.String("namespace", "", "namespace of the resource")
	kind := c.fs.String("kind", "", "Kind or plural resource name, to break ties")
	depth := c.fs.Int("depth", graph.DefaultDepth, "containment levels below the focus (1-3)")
	budget := c.fs.Int("budget", graph.DefaultBudget, "maximum output bytes for the map (>= 1024; the footer is reserved on top)")
	pos, code, done := c.parse(args, io)
	if done {
		return code
	}
	if len(pos) != 1 {
		fmt.Fprintf(io.Stderr, "kscope map: expected exactly one <name>, got %d\nrun 'kscope map --help' for usage\n", len(pos))
		return ExitError
	}

	if code := checkNamespace("map", *ns, io); code != ExitOK {
		return code
	}
	// Neighbourhood checks the range too, but only once a focus has been
	// resolved. Saying it here means `map ghost --depth 9` reports the
	// argument error rather than a miss that sends the caller off to fix the
	// name and hit the same wall again. 0 keeps its zero-value meaning:
	// "use the default".
	if *depth < 0 || *depth > graph.MaxDepth {
		fmt.Fprintf(io.Stderr, "kscope map: --depth must be between 1 and %d; got %d\n", graph.MaxDepth, *depth)
		return ExitError
	}

	snap, _, code := loadSnapshot(c.dataDir, io)
	if code != ExitOK {
		return code
	}

	// Everything printed after the projection is reserved out of the budget
	// so the whole of stdout, not just the tree, honours --budget. It depends
	// on the snapshot, not on the focus, so the floor below can be — and is —
	// checked before resolution, for the same reason as --depth above.
	tail := fmt.Sprintf("\n%d nodes / %d edges in scope\n%s", len(snap.Nodes), len(snap.Edges), footer(snap, c.dataDir))

	// graph.MinBudget (1024, the spec's documented minimum) is the floor for
	// the map projection alone. Neighbourhood also reserves `tail` out of
	// whatever --budget it is given, so --budget 1024 always fails there with
	// the graph-level ErrBudget — true, but it doesn't name the number that
	// would actually work for this snapshot. Check up front so the error is
	// useful instead of just correct.
	if *budget < graph.MinBudget+len(tail) {
		fmt.Fprintf(io.Stderr, "kscope map: --budget must be at least %d for this snapshot (%d for the map plus %d for the footer)\n",
			graph.MinBudget+len(tail), graph.MinBudget, len(tail))
		return ExitError
	}

	focus, code := resolveOne(snap, graph.NodeRef{Namespace: *ns, Name: pos[0], Kind: *kind}, c.dataDir, io)
	if code != ExitOK {
		return code
	}

	r, err := graph.Neighbourhood(snap, focus.ID, graph.ViewOptions{Depth: *depth, Budget: *budget, Reserve: len(tail)})
	if err != nil {
		// The check above names the floor for the common case. ErrSkeleton is
		// the case it cannot predict: this focus sits deep enough that its own
		// ancestor chain and focus line are longer than that floor, and those
		// are never truncated — so nothing but a larger --budget helps, and
		// how much larger depends on the resource.
		hint := ""
		if errors.Is(err, graph.ErrSkeleton) {
			hint = fmt.Sprintf(" — the ancestors of %s %s and its own line do not fit; retry with a --budget above %d",
				graph.Sanitize(focus.Kind, graph.ShortText), graph.Sanitize(focus.Name, 0), *budget)
		}
		fmt.Fprintf(io.Stderr, "kscope map: %v%s\n", err, hint)
		return ExitError
	}
	fmt.Fprint(io.Stdout, r.Text, tail)
	return ExitOK
}
