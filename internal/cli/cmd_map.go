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
(default 8192, minimum 1024). Flags may come before or after the name.

Exit 0: printed. Exit 2: no match or ambiguous — stderr names the fix.`

func init() {
	register(command{name: "map", synopsis: mapSynopsis, run: runMap})
}

func runMap(args []string, io IO) int {
	c := newFlags("map", mapSynopsis, io)
	ns := c.fs.String("namespace", "", "namespace of the resource")
	kind := c.fs.String("kind", "", "Kind or plural resource name, to break ties")
	depth := c.fs.Int("depth", graph.DefaultDepth, "containment levels below the focus (1-3)")
	budget := c.fs.Int("budget", graph.DefaultBudget, "maximum output bytes (>= 1024)")
	pos, code, done := c.parse(args, io)
	if done {
		return code
	}
	if len(pos) != 1 {
		fmt.Fprintf(io.Stderr, "kscope map: expected exactly one <name>, got %d\nrun 'kscope map --help' for usage\n", len(pos))
		return ExitError
	}

	snap, _, code := loadSnapshot(c.dataDir, io)
	if code != ExitOK {
		return code
	}
	focus, code := resolveOne(snap, graph.NodeRef{Namespace: *ns, Name: pos[0], Kind: *kind}, c.dataDir, io)
	if code != ExitOK {
		return code
	}

	// Everything printed after the projection is reserved out of the budget
	// so the whole of stdout, not just the tree, honours --budget.
	tail := fmt.Sprintf("\n%d nodes / %d edges in scope\n%s", len(snap.Nodes), len(snap.Edges), footer(snap, c.dataDir))
	r, err := graph.Neighbourhood(snap, focus.ID, graph.ViewOptions{Depth: *depth, Budget: *budget, Reserve: len(tail)})
	switch {
	case errors.Is(err, graph.ErrDepth), errors.Is(err, graph.ErrBudget), errors.Is(err, graph.ErrSkeleton):
		fmt.Fprintf(io.Stderr, "kscope map: %v\n", err)
		return ExitError
	case err != nil:
		fmt.Fprintf(io.Stderr, "kscope map: %v\n", err)
		return ExitError
	}
	fmt.Fprint(io.Stdout, r.Text, tail)
	return ExitOK
}
