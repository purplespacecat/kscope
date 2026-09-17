package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
)

const findSynopsis = `kscope find [--name-contains S] [--kind K] [--namespace NS] [--health H] [--limit N] [--data-dir DIR]

List resources in the snapshot. --kind accepts a Kind (Deployment) or its
plural (deployments). With no filters, an inventory up to --limit.

Exit 2 means a name, kind or namespace the snapshot does not have. A --health
filter matching nothing is an answer, not a miss: it exits 0 with a zero count,
so 'find --health error' finding none means nothing in scope is unhealthy.`

func init() {
	register(command{name: "find", summary: "list resources in the snapshot, filtered by name, kind, namespace or health", synopsis: findSynopsis, run: runFind})
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
	if code := checkNamespace("find", *ns, io); code != ExitOK {
		return code
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

	// identityHits counts nodes passing the name/kind/namespace filters alone,
	// before --health narrows them. The two kinds of "nothing" mean different
	// things and only one of them is a miss — see below.
	var matches []graph.Node
	identityHits := 0
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
		identityHits++
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

	// Exit 2 means "widen and retry", so it belongs only to a query that named
	// something the snapshot does not have. --name-contains, --kind and
	// --namespace name things; --health asks a question about whatever is in
	// scope, and "none" is a real answer to it. Treating those alike made
	// `find --health error` report that nothing is broken by printing an
	// out-of-scope refresh hint and failing — an agent reasonably read that as
	// an inconclusive lookup rather than a clean bill of health.
	namedSomething := *contains != "" || *kind != "" || *ns != ""
	if namedSomething && identityHits == 0 {
		what := describe(*contains, *kind, *ns, *health)
		fmt.Fprint(io.Stderr, missMessage(what, snap, c.dataDir, *ns))
		return ExitMiss
	}

	shown := matches
	if len(shown) > *limit {
		shown = shown[:*limit]
	}
	// Kind, name, namespace and reason all come from the snapshot, which
	// carries strings kscope did not author (a container runtime's waiting
	// reason, a CRD's spec.names.kind). Filter them before they reach an
	// agent's context or a terminal — see graph.Sanitize.
	for _, n := range shown {
		nsCol := ""
		if n.Namespace != "" {
			nsCol = "-n " + graph.Sanitize(n.Namespace, 0)
		}
		right := graph.HealthGlyph(n.Health)
		if n.Reason != "" {
			right += " " + graph.Sanitize(n.Reason, graph.ShortText)
		}
		fmt.Fprintf(io.Stdout, "%-16s %-40s %-24s %s\n", graph.Sanitize(n.Kind, graph.ShortText), graph.Sanitize(n.Name, 0), nsCol, right)
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
	// These are echoed straight back from the command line, so they get the
	// same filter as snapshot-derived text.
	var parts []string
	if contains != "" {
		parts = append(parts, "name~"+graph.Sanitize(contains, graph.ShortText))
	}
	if kind != "" {
		parts = append(parts, "kind="+graph.Sanitize(kind, graph.ShortText))
	}
	if ns != "" {
		parts = append(parts, "namespace="+graph.Sanitize(ns, graph.ShortText))
	}
	if health != "" {
		parts = append(parts, "health="+graph.Sanitize(health, graph.ShortText))
	}
	return strings.Join(parts, " ")
}
