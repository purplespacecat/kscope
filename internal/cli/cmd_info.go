package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
)

const infoSynopsis = `kscope info [--data-dir DIR]

Describe the loaded snapshot: cluster, scope, age, counts, discovery errors.`

func init() {
	register(command{name: "info", summary: "describe the loaded snapshot: cluster, scope, age, counts, discovery errors", synopsis: infoSynopsis, run: runInfo})
}

func runInfo(args []string, io IO) int {
	c := newFlags("info", infoSynopsis, io)
	if _, code, done := c.parse(args, io); done {
		return code
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
	ver := cl.Version
	if cl.Distro != "" {
		ver += ", " + cl.Distro
	}
	fmt.Fprintf(&sb, "cluster   %s (%s)\n", cl.Context, ver)
	if cl.Server != "" {
		fmt.Fprintf(&sb, "server    %s\n", cl.Server)
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
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = k.kind + " " + strconv.Itoa(k.n)
	}
	fmt.Fprintf(&sb, "kinds     %s\n", strings.Join(parts, ", "))

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
		out[i] = "(" + strconv.Itoa(count[e]) + "×) " + e
	}
	return out
}
