package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
)

// kindsBudget bounds the "kinds" line in bytes, the way everything else this
// tool prints is bounded. A count-based cap would not hold: twelve kinds is one
// line of Pods and Deployments but 333 characters of Crossplane
// (ManagedResourceDefinition, CompositeResourceDefinition, ...), and the name
// lengths are the cluster's to choose, not ours.
const kindsBudget = 200

const infoSynopsis = `kscope info [--data-dir DIR]

Describe the loaded snapshot: cluster, scope, age, counts, discovery errors.`

func init() {
	register(command{name: "info", summary: "describe the loaded snapshot: cluster, scope, age, counts, discovery errors", synopsis: infoSynopsis, run: runInfo})
}

func runInfo(args []string, io IO) int {
	c := newFlags("info", infoSynopsis, io)
	pos, code, done := c.parse(args, io)
	if done {
		return code
	}
	// info takes no positionals. Accepting and ignoring them would make
	// `kscope info web-1` print the snapshot summary and exit 0, which reads
	// as an answer about web-1; find rejects strays and map and manifest
	// require exactly one, so this is the same contract.
	if len(pos) > 0 {
		fmt.Fprintf(io.Stderr, "kscope info: unexpected argument %q\n", pos[0])
		return ExitError
	}
	snap, _, code := loadSnapshot(c.dataDir, io)
	if code != ExitOK {
		return code
	}
	fmt.Fprint(io.Stdout, infoText(snap, c.dataDir))
	return ExitOK
}

// infoText is the cold-start orientation an agent reads before anything
// else. Discovery errors are summarised: the reference snapshot carries
// 1,160 of them, three-quarters of the file.
func infoText(snap graph.Snapshot, dataDir string) string {
	var sb strings.Builder
	cl := snap.Cluster
	// Everything on these lines comes from the snapshot, including strings
	// the API server or a CRD author chose; filter them (graph.Sanitize).
	ver := graph.Sanitize(cl.Version, graph.ShortText)
	if cl.Distro != "" {
		ver += ", " + graph.Sanitize(cl.Distro, graph.ShortText)
	}
	// snapshotContext, not cl.Context: the footer uses it, and a snapshot
	// that recorded only what was asked for would otherwise print a blank
	// cluster line above a populated footer.
	fmt.Fprintf(&sb, "cluster   %s (%s)\n", snapshotContext(snap), ver)
	if cl.Server != "" {
		fmt.Fprintf(&sb, "server    %s\n", graph.Sanitize(cl.Server, 0))
	}
	fmt.Fprintf(&sb, "scope     ns=%s infra=%t crds=%t\n", nsList(snap.Scope), snap.Scope.IncludeInfra, snap.Scope.IncludeCRDs)
	fmt.Fprintf(&sb, "captured  %s (%s ago) in %dms\n", snap.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), humanAge(now().Sub(snap.Timestamp)), snap.Stats.DurationMs)
	fmt.Fprintf(&sb, "%d nodes / %d edges\n", len(snap.Nodes), len(snap.Edges))

	type kc struct {
		kind string
		n    int
	}
	var kinds []kc
	for k, n := range snap.Stats.Counts {
		kinds = append(kinds, kc{k, n})
	}
	sort.Slice(kinds, func(i, j int) bool {
		if kinds[i].n != kinds[j].n {
			return kinds[i].n > kinds[j].n
		}
		return kinds[i].kind < kinds[j].kind
	})
	// Only the busiest kinds are named, and only as many as the budget affords.
	// A cluster running Crossplane or Kyverno has hundreds of custom kinds in
	// one snapshot — 137 of them on the reference cluster, which rendered as a
	// single 2,750-character line. This command exists to orient an agent
	// cheaply, so the tail becomes a count.
	//
	// Kinds are taken while they fit, leaving room for the "+N more" tail the
	// remainder will need. The tail is measured against the worst case (every
	// remaining kind omitted) so adding one more kind can never overflow the
	// budget by growing the suffix.
	named, tailNodes := 0, 0
	for _, k := range kinds {
		tailNodes += k.n
	}
	used := 0
	for i, k := range kinds {
		part := graph.Sanitize(k.kind, graph.ShortText) + " " + strconv.Itoa(k.n)
		width := len(part)
		if i > 0 {
			width += len(", ")
		}
		suffix := 0
		if rest := len(kinds) - i - 1; rest > 0 {
			suffix = len(fmt.Sprintf(", … +%d more kinds (%d nodes)", rest, tailNodes))
		}
		if used+width+suffix > kindsBudget {
			break
		}
		used += width
		named++
		tailNodes -= k.n
	}
	// Always name at least one kind, however long its name: a line that says
	// only "+137 more kinds" tells an agent nothing about what is in there.
	if named == 0 && len(kinds) > 0 {
		named, tailNodes = 1, 0
		for _, k := range kinds[1:] {
			tailNodes += k.n
		}
	}
	parts := make([]string, named)
	for i, k := range kinds[:named] {
		parts[i] = graph.Sanitize(k.kind, graph.ShortText) + " " + strconv.Itoa(k.n)
	}
	line := strings.Join(parts, ", ")
	if rest := len(kinds) - named; rest > 0 {
		line += fmt.Sprintf(", … +%d more kinds (%d nodes)", rest, tailNodes)
	}
	fmt.Fprintf(&sb, "kinds     %s\n", line)

	if n := len(snap.Stats.Errors); n > 0 {
		top := topErrors(snap.Stats.Errors, 5)
		fmt.Fprintf(&sb, "errors    %d — %d most frequent:\n", n, len(top))
		for _, l := range top {
			sb.WriteString("  " + l + "\n")
		}
	}
	sb.WriteString(footer(snap, dataDir))
	return sb.String()
}

// topErrors returns the n most frequent distinct messages as "(k×) msg".
// Construction already yields first-occurrence order and SliceStable preserves it,
// so the comparator's tie branch states the rule explicitly rather than deciding it today;
// it exists so a future rewrite of how order is built cannot silently change the output.
func topErrors(errs []string, n int) []string {
	count := map[string]int{}
	first := map[string]int{}
	var order []string
	for i, e := range errs {
		if _, seen := count[e]; !seen {
			first[e] = i
			order = append(order, e)
		}
		count[e]++
	}
	sort.SliceStable(order, func(i, j int) bool {
		if count[order[i]] != count[order[j]] {
			return count[order[i]] > count[order[j]]
		}
		return first[order[i]] < first[order[j]]
	})
	if len(order) > n {
		order = order[:n]
	}
	out := make([]string, len(order))
	for i, e := range order {
		// Uncapped: a discovery error is a whole sentence and truncating it
		// would lose the part that says which API call failed.
		out[i] = "(" + strconv.Itoa(count[e]) + "×) " + graph.Sanitize(e, 0)
	}
	return out
}
