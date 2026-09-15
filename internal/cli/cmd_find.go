package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
)

const findSynopsis = `kscope find [--name-contains S] [--kind K] [--namespace NS] [--health H] [--limit N] [--data-dir DIR]

List resources in the snapshot. --kind accepts a Kind (Deployment) or its
plural (deployments). With no filters, an inventory up to --limit.`

func init() {
	register(command{name: "find", synopsis: findSynopsis, run: runFind})
}

func runFind(args []string, io IO) int {
	c := newFlags("find", findSynopsis, io)
	contains := c.fs.String("name-contains", "", "case-insensitive substring of the name")
	kind := c.fs.String("kind", "", "Kind or plural resource name")
	ns := c.fs.String("namespace", "", "exact namespace")
	health := c.fs.String("health", "", "healthy | warning | error | unknown")
	limit := c.fs.Int("limit", 50, "maximum lines")
	pos, code, done := c.parse(args, io)
	if done {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintf(io.Stderr, "kscope find: unexpected argument %q\n", pos[0])
		return ExitError
	}
	if *limit < 1 {
		fmt.Fprintf(io.Stderr, "kscope find: --limit must be at least 1; got %d\n", *limit)
		return ExitError
	}
	var wantHealth graph.Health
	if *health != "" {
		switch h := graph.Health(*health); h {
		case graph.HealthHealthy, graph.HealthWarning, graph.HealthError, graph.HealthUnknown:
			wantHealth = h
		default:
			fmt.Fprintf(io.Stderr, "kscope find: --health must be healthy, warning, error or unknown; got %q\n", *health)
			return ExitError
		}
	}

	snap, _, code := loadSnapshot(c.dataDir, io)
	if code != ExitOK {
		return code
	}

	var matches []graph.Node
	for _, n := range snap.Nodes {
		if *contains != "" && !strings.Contains(strings.ToLower(n.Name), strings.ToLower(*contains)) {
			continue
		}
		if *kind != "" && !graph.KindMatches(n.Kind, *kind) {
			continue
		}
		if *ns != "" && n.Namespace != *ns {
			continue
		}
		if wantHealth != "" && n.Health != wantHealth {
			continue
		}
		matches = append(matches, n)
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})

	filtered := *contains != "" || *kind != "" || *ns != "" || wantHealth != ""
	if len(matches) == 0 && filtered {
		what := describe(*contains, *kind, *ns, *health)
		fmt.Fprint(io.Stderr, missMessage(what, snap, c.dataDir, *ns))
		return ExitMiss
	}

	shown := matches
	if len(shown) > *limit {
		shown = shown[:*limit]
	}
	for _, n := range shown {
		nsCol := ""
		if n.Namespace != "" {
			nsCol = "-n " + n.Namespace
		}
		right := graph.HealthGlyph(n.Health)
		if n.Reason != "" {
			right += " " + n.Reason
		}
		fmt.Fprintf(io.Stdout, "%-16s %-40s %-24s %s\n", n.Kind, n.Name, nsCol, right)
	}
	if len(shown) < len(matches) {
		fmt.Fprintf(io.Stdout, "showing %d of %d matches (of %d nodes)\n", len(shown), len(matches), len(snap.Nodes))
	} else {
		fmt.Fprintf(io.Stdout, "%d matches (of %d nodes)\n", len(matches), len(snap.Nodes))
	}
	fmt.Fprint(io.Stdout, footer(snap, c.dataDir))
	return ExitOK
}

// describe renders the filters for the miss message: "name~cube kind=pods".
func describe(contains, kind, ns, health string) string {
	var parts []string
	if contains != "" {
		parts = append(parts, "name~"+contains)
	}
	if kind != "" {
		parts = append(parts, "kind="+kind)
	}
	if ns != "" {
		parts = append(parts, "namespace="+ns)
	}
	if health != "" {
		parts = append(parts, "health="+health)
	}
	return strings.Join(parts, " ")
}
