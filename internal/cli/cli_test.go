package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/purplespacecat/kscope/internal/graph"
)

type bufs struct{ out, err bytes.Buffer }

func (b *bufs) io() IO { return IO{Stdout: &b.out, Stderr: &b.err} }

// writeSnapshot persists a snapshot the way the server does, so the CLI
// reads exactly what the desktop app would have written.
func writeSnapshot(t *testing.T, dir string, snap graph.Snapshot) {
	t.Helper()
	if err := graph.NewStore(filepath.Join(dir, "latest.json")).Set(snap); err != nil {
		t.Fatal(err)
	}
}

func pinClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = prev })
}

func TestRun_UnknownCommandIsExit1(t *testing.T) {
	var b bufs
	if code := Run([]string{"mpa", "foo"}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want %d", code, ExitError)
	}
	if !strings.Contains(b.err.String(), `unknown command "mpa"`) || b.out.Len() != 0 {
		t.Fatalf("stderr=%q stdout=%q", b.err.String(), b.out.String())
	}
}

func TestRun_HelpGoesToStdout(t *testing.T) {
	var b bufs
	if code := Run([]string{"info", "--help"}, b.io()); code != ExitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(b.out.String(), "kscope info") || b.err.Len() != 0 {
		t.Fatalf("usage must go to stdout only: stdout=%q stderr=%q", b.out.String(), b.err.String())
	}
}

func TestRun_BadFlagIsExit1NotExit2(t *testing.T) {
	// ExitOnError would make this exit 2 — the code that means "widen the
	// scope and retry". A typo must not send an agent off to re-discover.
	var b bufs
	if code := Run([]string{"info", "--bogus"}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want %d", code, ExitError)
	}
	if b.out.Len() != 0 || !strings.Contains(b.err.String(), "bogus") {
		t.Fatalf("stdout=%q stderr=%q", b.out.String(), b.err.String())
	}
}

func TestRun_EmptyStoreIsExit3(t *testing.T) {
	var b bufs
	dir := t.TempDir()
	if code := Run([]string{"info", "--data-dir", dir}, b.io()); code != ExitNoSnapshot {
		t.Fatalf("code = %d, want %d", code, ExitNoSnapshot)
	}
	if !strings.Contains(b.err.String(), "no snapshot") || !strings.Contains(b.err.String(), "--discover-namespaces") {
		t.Fatalf("stderr must name the fix: %q", b.err.String())
	}
}

func TestParse_FlagsAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"web", "--kind", "deployment"},
		{"--kind", "deployment", "web"},
		{"--kind=deployment", "web"},
	} {
		var b bufs
		c := newFlags("map", "kscope map <name>", b.io())
		kind := c.fs.String("kind", "", "")
		pos, code, done := c.parse(args, b.io())
		if done {
			t.Fatalf("%v: unexpected early exit %d: %s", args, code, b.err.String())
		}
		if len(pos) != 1 || pos[0] != "web" || *kind != "deployment" {
			t.Fatalf("%v: pos=%v kind=%q", args, pos, *kind)
		}
	}
}

func TestHumanAge(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:             "45s",
		7 * time.Minute:              "7m",
		4*time.Hour + 12*time.Minute: "4h12m",
		3*24*time.Hour + 2*time.Hour: "3d2h",
	}
	for d, want := range cases {
		if got := humanAge(d); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFooter(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	snap := graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Context: "dev/ci1", Namespaces: []string{"app", "ops"}},
	}
	got := footer(snap, "/data")
	want := "snapshot 4h12m old · context=dev/ci1 · ns=[app,ops] · data=/data\n"
	if got != want {
		t.Fatalf("footer = %q, want %q", got, want)
	}
	snap.Scope.Namespaces = nil
	if !strings.Contains(footer(snap, "/data"), "ns=[all]") {
		t.Fatalf("empty scope must render as all: %q", footer(snap, "/data"))
	}
}

func TestFooter_FallsBackToClusterContext(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	// Scope.Context is empty whenever discovery ran against the kubeconfig's
	// current-context (no --context passed); Cluster.Context always holds the
	// resolved name and must be what the footer shows.
	snap := graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Namespaces: []string{"app", "ops"}},
		Cluster:   graph.ClusterMeta{Context: "dev/ci1"},
	}
	got := footer(snap, "/data")
	want := "snapshot 4h12m old · context=dev/ci1 · ns=[app,ops] · data=/data\n"
	if got != want {
		t.Fatalf("footer = %q, want %q", got, want)
	}
}

func infoSnapshot() graph.Snapshot {
	errs := []string{}
	for i := 0; i < 700; i++ {
		errs = append(errs, "list pods in ns a: rate limited")
	}
	for i := 0; i < 300; i++ {
		errs = append(errs, "list secrets in ns a: rate limited")
	}
	// Four messages that each occur once, in this order; two must make the
	// top five and be listed in first-occurrence order.
	errs = append(errs, "crds: forbidden", "nodes: forbidden", "pv: forbidden", "sc: forbidden")
	for i := 0; i < 156; i++ {
		errs = append(errs, "list configmaps in ns a: rate limited")
	}
	return graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Context: "dev/ci1", Namespaces: []string{"a"}, IncludeInfra: true, IncludeCRDs: true},
		Cluster:   graph.ClusterMeta{Context: "dev/ci1", Server: "https://k8s.example", Version: "v1.34.9-eks", Distro: ""},
		Nodes:     []graph.Node{{ID: "n1", Kind: "Pod"}, {ID: "n2", Kind: "Pod"}, {ID: "n3", Kind: "Secret"}},
		Edges:     []graph.Edge{{ID: "e1"}},
		Stats:     graph.Stats{Counts: map[string]int{"Pod": 2, "Secret": 1}, DurationMs: 30012, Errors: errs},
	}
}

func TestInfo_SummarisesErrorsInsteadOfListingThem(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, infoSnapshot())
	var b bufs
	if code := Run([]string{"info", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	out := b.out.String()
	for _, want := range []string{
		"cluster   dev/ci1 (v1.34.9-eks)",
		"scope     ns=[a] infra=true crds=true",
		"3 nodes / 1 edges",
		"kinds     Pod 2, Secret 1",
		"errors    1160 — 5 most frequent:",
		"(700×) list pods in ns a: rate limited",
		"(300×) list secrets in ns a: rate limited",
		"(156×) list configmaps in ns a: rate limited",
		"(1×) crds: forbidden",
		"(1×) nodes: forbidden",
		"snapshot 4h12m old · context=dev/ci1 · ns=[a] · data=" + dir,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Assert the order of error messages: by count descending, then first occurrence.
	idx700 := strings.Index(out, "(700×) list pods in ns a: rate limited")
	idx300 := strings.Index(out, "(300×) list secrets in ns a: rate limited")
	idx156 := strings.Index(out, "(156×) list configmaps in ns a: rate limited")
	idxCrds := strings.Index(out, "(1×) crds: forbidden")
	idxNodes := strings.Index(out, "(1×) nodes: forbidden")
	if idx700 < 0 || idx300 < 0 || idx156 < 0 || idxCrds < 0 || idxNodes < 0 {
		t.Fatalf("could not find all error messages")
	}
	if idx700 >= idx300 {
		t.Fatalf("(700×) should precede (300×), got idx700=%d idx300=%d", idx700, idx300)
	}
	if idx300 >= idx156 {
		t.Fatalf("(300×) should precede (156×), got idx300=%d idx156=%d", idx300, idx156)
	}
	if idxCrds >= idxNodes {
		t.Fatalf("(1×) crds: forbidden should precede (1×) nodes: forbidden, got idxCrds=%d idxNodes=%d", idxCrds, idxNodes)
	}
	if strings.Contains(out, "pv: forbidden") {
		t.Fatalf("only five messages may be listed:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > 14 {
		t.Fatalf("info must stay short, got %d lines:\n%s", n, out)
	}
	if b.err.Len() != 0 {
		t.Fatalf("stderr must be empty on success: %q", b.err.String())
	}
}

// info takes no positionals, so a stray one is exit 1 like find's. Accepting
// it would answer `kscope info web-1` with the snapshot summary and exit 0,
// which reads as an answer about web-1.
func TestInfo_RejectsStrayPositionals(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, infoSnapshot())
	var b bufs
	if code := Run([]string{"info", "web-1", "extra", "--data-dir", dir}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want %d", code, ExitError)
	}
	if b.out.Len() != 0 {
		t.Fatalf("stdout must stay empty: %q", b.out.String())
	}
	if !strings.Contains(b.err.String(), `unexpected argument "web-1"`) {
		t.Fatalf("stderr = %q", b.err.String())
	}
}

// The cluster line and the footer must name the same context. Cluster.Context
// is what discovery reached and Scope.Context what it was asked for; a
// snapshot carrying only the latter used to print a blank cluster line above
// a populated footer.
func TestInfo_ClusterLineAgreesWithTheFooter(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	snap := infoSnapshot()
	snap.Cluster.Context = "" // partial snapshot: only the requested context survived
	writeSnapshot(t, dir, snap)
	var b bufs
	if code := Run([]string{"info", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	out := b.out.String()
	if !strings.Contains(out, "cluster   dev/ci1 (") {
		t.Fatalf("cluster line does not name the context:\n%s", out)
	}
	if !strings.Contains(out, "context=dev/ci1") {
		t.Fatalf("footer does not name the context:\n%s", out)
	}
}

func findSnapshot() graph.Snapshot {
	return graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Context: "dev/ci1", Namespaces: []string{"app"}, IncludeInfra: true, IncludeCRDs: false},
		Cluster:   graph.ClusterMeta{Context: "dev/ci1"},
		Nodes: []graph.Node{
			{ID: "cluster", Kind: "Cluster", Name: "dev/ci1", Health: graph.HealthHealthy},
			{ID: "core/namespace/app", Kind: "Namespace", Name: "app", Health: graph.HealthHealthy},
			{ID: "apps/deployment/app/web", Kind: "Deployment", Name: "web", Namespace: "app", Health: graph.HealthHealthy},
			{ID: "core/service/app/web", Kind: "Service", Name: "web", Namespace: "app", Health: graph.HealthHealthy},
			{ID: "core/pod/app/web-1", Kind: "Pod", Name: "web-1", Namespace: "app", Health: graph.HealthError, Reason: "CrashLoopBackOff"},
			{ID: "core/pod/app/web-2", Kind: "Pod", Name: "web-2", Namespace: "app", Health: graph.HealthHealthy},
		},
	}
}

func TestFind_FiltersAndFormats(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, findSnapshot())

	var b bufs
	if code := Run([]string{"find", "--name-contains", "web", "--kind", "pods", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	out := b.out.String()
	if !strings.Contains(out, "Pod") || !strings.Contains(out, "web-1") || !strings.Contains(out, "✗ CrashLoopBackOff") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, "Deployment") {
		t.Fatalf("--kind pods must exclude the Deployment:\n%s", out)
	}
	if !strings.Contains(out, "2 matches (of 6 nodes)") {
		t.Fatalf("missing totals line:\n%s", out)
	}
	if !strings.Contains(out, "snapshot 4h12m old") {
		t.Fatalf("missing footer:\n%s", out)
	}

	b = bufs{}
	if code := Run([]string{"find", "--health", "error", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	if !strings.Contains(b.out.String(), "1 matches") || strings.Contains(b.out.String(), "web-2") {
		t.Fatalf("--health error should keep only web-1:\n%s", b.out.String())
	}

	b = bufs{}
	if code := Run([]string{"find", "--limit", "2", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	if !strings.Contains(b.out.String(), "showing 2 of 6 matches") {
		t.Fatalf("limit must be reported:\n%s", b.out.String())
	}
}

func TestFind_MissIsExit2WithTheFix(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, findSnapshot())
	var b bufs
	if code := Run([]string{"find", "--name-contains", "cube", "--namespace", "cube", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d", code, ExitMiss)
	}
	if b.out.Len() != 0 {
		t.Fatalf("miss must leave stdout empty: %q", b.out.String())
	}
	e := b.err.String()
	for _, want := range []string{
		"No resource matching",
		"context=dev/ci1 ns=[app] (4h12m old)",
		"kscope --context dev/ci1 --data-dir " + dir,
		"--discover-namespaces=app,cube",
		"--include-infra=true --include-crds=false",
	} {
		if !strings.Contains(e, want) {
			t.Errorf("stderr missing %q:\n%s", want, e)
		}
	}
}

func TestFind_BadHealthIsExit1(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, findSnapshot())
	var b bufs
	if code := Run([]string{"find", "--health", "meh", "--data-dir", dir}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestFind_BadLimitIsExit1(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, findSnapshot())

	for _, limit := range []string{"-1", "0"} {
		var b bufs
		if code := Run([]string{"find", "--limit", limit, "--data-dir", dir}, b.io()); code != ExitError {
			t.Fatalf("--limit %s: code = %d, want %d", limit, code, ExitError)
		}
		if b.out.Len() != 0 {
			t.Fatalf("--limit %s: stdout must be empty, got %q", limit, b.out.String())
		}
		if !strings.Contains(b.err.String(), "--limit") {
			t.Fatalf("--limit %s: stderr must name --limit, got %q", limit, b.err.String())
		}
	}

	// A bad --limit must be caught before the store is read: exit 1 even
	// against an empty data dir that would otherwise yield exit 3.
	var b bufs
	empty := t.TempDir()
	if code := Run([]string{"find", "--limit", "-1", "--data-dir", empty}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want %d (must precede loadSnapshot)", code, ExitError)
	}
	if b.out.Len() != 0 {
		t.Fatalf("stdout must be empty, got %q", b.out.String())
	}
	if !strings.Contains(b.err.String(), "--limit") {
		t.Fatalf("stderr must name --limit, got %q", b.err.String())
	}
}

func TestRefreshHint_AllNamespaces(t *testing.T) {
	snap := findSnapshot()
	snap.Scope.Namespaces = nil
	hint := refreshHint(snap, "/d", "cube")
	if !strings.Contains(hint, "--discover-all-namespaces") || strings.Contains(hint, "--discover-namespaces=") {
		t.Fatalf("all-namespace scope must refresh with --discover-all-namespaces: %q", hint)
	}
}

func TestRefreshHint_FallsBackToClusterContext(t *testing.T) {
	// Scope.Context is empty whenever --context was not passed to discovery;
	// Cluster.Context always holds the resolved name and must be used so the
	// refresh command never targets a blank --context.
	snap := findSnapshot()
	snap.Scope.Context = ""
	hint := refreshHint(snap, "/d", "")
	if !strings.Contains(hint, "--context dev/ci1") {
		t.Fatalf("refreshHint must fall back to Cluster.Context: %q", hint)
	}
}

// mapSnapshot: Cluster → ns app → Deployment web → ReplicaSet → pods, a
// Service also named web (for ambiguity), a Secret the pods mount, and a
// synthetic control-plane node (for manifest). pods controls fan-out.
func mapSnapshot(pods int) graph.Snapshot {
	n := []graph.Node{
		{ID: "cluster", Kind: "Cluster", Name: "dev/ci1", Health: graph.HealthHealthy, Synthetic: true},
		{ID: "core/namespace/app", Kind: "Namespace", Name: "app", ParentID: "cluster", Health: graph.HealthHealthy},
		{ID: "apps/deployment/app/web", Kind: "Deployment", Name: "web", Namespace: "app", ParentID: "core/namespace/app", Health: graph.HealthHealthy,
			Kubectl: "kubectl --context dev/ci1 -n app get deployment web -o yaml"},
		{ID: "apps/replicaset/app/web-rs", Kind: "ReplicaSet", Name: "web-rs", Namespace: "app", ParentID: "apps/deployment/app/web", Health: graph.HealthHealthy},
		{ID: "core/service/app/web", Kind: "Service", Name: "web", Namespace: "app", ParentID: "core/namespace/app", Health: graph.HealthHealthy},
		{ID: "core/secret/app/db", Kind: "Secret", Name: "db", Namespace: "app", ParentID: "core/namespace/app", Health: graph.HealthHealthy},
	}
	var e []graph.Edge
	for i := 0; i < pods; i++ {
		id := fmt.Sprintf("core/pod/app/web-rs-%03d", i)
		n = append(n, graph.Node{ID: id, Kind: "Pod", Name: fmt.Sprintf("web-rs-%03d", i), Namespace: "app", ParentID: "apps/replicaset/app/web-rs", Health: graph.HealthHealthy})
		e = append(e, graph.Edge{ID: id + " mounts", Kind: graph.EdgeMounts, Source: id, Target: "core/secret/app/db"})
	}
	return graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Context: "dev/ci1", Namespaces: []string{"app"}, IncludeInfra: true, IncludeCRDs: true},
		Cluster:   graph.ClusterMeta{Context: "dev/ci1"},
		Nodes:     n,
		Edges:     e,
		Manifests: map[string]string{"apps/deployment/app/web": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n"},
	}
}

func TestMap_RendersTheNeighbourhood(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(2))
	for _, args := range [][]string{
		{"map", "web", "--kind", "deployment", "--data-dir", dir},
		{"map", "--kind", "deployment", "--data-dir", dir, "web"},
	} {
		var b bufs
		if code := Run(args, b.io()); code != ExitOK {
			t.Fatalf("%v: code = %d: %s", args, code, b.err.String())
		}
		out := b.out.String()
		for _, want := range []string{
			"Cluster dev/ci1", "└─ ns app", "Deployment web", "✓ healthy",
			"kubectl --context dev/ci1 -n app get deployment web -o yaml",
			"ReplicaSet web-rs", "Pod web-rs-000", "mounts → Secret db",
			"\n8 nodes / 2 edges in scope\n",
			"snapshot 4h12m old · context=dev/ci1 · ns=[app] · data=" + dir + "\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: missing %q in:\n%s", args, want, out)
			}
		}
		if !strings.HasSuffix(out, "data="+dir+"\n") {
			t.Fatalf("footer must be the last line:\n%s", out)
		}
		if b.err.Len() != 0 {
			t.Fatalf("stderr must be empty on success: %q", b.err.String())
		}
	}
}

func TestMap_AmbiguousIsExit2WithCandidates(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	var b bufs
	// Deployment and Service are both "web" in ns app.
	if code := Run([]string{"map", "web", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d", code, ExitMiss)
	}
	if b.out.Len() != 0 {
		t.Fatalf("ambiguity must print no map: %q", b.out.String())
	}
	e := b.err.String()
	for _, want := range []string{"Ambiguous: 'web' matches 2 resources", "Add --kind", "Deployment", "Service", "-n app"} {
		if !strings.Contains(e, want) {
			t.Errorf("stderr missing %q:\n%s", want, e)
		}
	}
}

func TestMap_MissIsExit2(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	var b bufs
	if code := Run([]string{"map", "cube", "--namespace", "cube", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d", code, ExitMiss)
	}
	if !strings.Contains(b.err.String(), "No resource matching name=cube namespace=cube") || !strings.Contains(b.err.String(), "--discover-namespaces=app,cube") {
		t.Fatalf("stderr: %q", b.err.String())
	}
}

// budgetPressureSnapshot builds a Deployment with rsCount ReplicaSets, each
// with podsPerRS Pods — deliberately kept below groupAt (3) per ReplicaSet so
// the Pod-grouping rollup never collapses them, and ReplicaSets themselves are
// never grouped (they have children, so allLeaves is false). Content therefore
// grows linearly with rsCount instead of being capped to a small "Kind (N)"
// summary the way mapSnapshot's single-parent pod fan-out is — this is what
// makes it possible to force real per-row truncation at a chosen budget.
func budgetPressureSnapshot(rsCount, podsPerRS int) graph.Snapshot {
	n := []graph.Node{
		{ID: "cluster", Kind: "Cluster", Name: "dev/ci1", Health: graph.HealthHealthy, Synthetic: true},
		{ID: "core/namespace/app", Kind: "Namespace", Name: "app", ParentID: "cluster", Health: graph.HealthHealthy},
		{ID: "apps/deployment/app/web", Kind: "Deployment", Name: "web", Namespace: "app", ParentID: "core/namespace/app", Health: graph.HealthHealthy,
			Kubectl: "kubectl --context dev/ci1 -n app get deployment web -o yaml"},
	}
	for i := 0; i < rsCount; i++ {
		rsID := fmt.Sprintf("apps/replicaset/app/web-rs-%03d", i)
		n = append(n, graph.Node{ID: rsID, Kind: "ReplicaSet", Name: fmt.Sprintf("web-rs-%03d", i), Namespace: "app", ParentID: "apps/deployment/app/web", Health: graph.HealthHealthy})
		for j := 0; j < podsPerRS; j++ {
			podID := fmt.Sprintf("core/pod/app/web-rs-%03d-%d", i, j)
			n = append(n, graph.Node{ID: podID, Kind: "Pod", Name: fmt.Sprintf("web-rs-%03d-%d", i, j), Namespace: "app", ParentID: rsID, Health: graph.HealthHealthy})
		}
	}
	return graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Context: "dev/ci1", Namespaces: []string{"app"}, IncludeInfra: true, IncludeCRDs: true},
		Cluster:   graph.ClusterMeta{Context: "dev/ci1"},
		Nodes:     n,
	}
}

func TestMap_BudgetCoversTheWholeStdout(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	// The brief's own draft of this test used mapSnapshot(80) at --budget 1024.
	// Neither survives: mapSnapshot's 80 Pods share one ReplicaSet parent, so
	// they collapse to a small fixed-size "Pods (80)" summary regardless of
	// budget (groupAt=3 triggers well before any byte limit is even
	// consulted) — content stays ~560 bytes, far under any valid budget, so
	// that fixture cannot actually exercise the Reserve arithmetic: it would
	// pass identically whether Reserve were wired correctly or not. And
	// --budget 1024 (graph.MinBudget) is not reachable at all once Reserve is
	// non-zero: Neighbourhood rejects any request where budget-Reserve<
	// MinBudget, and the footer is never empty (see graph.ErrSkeleton's test
	// in internal/graph/view_test.go and the guard added alongside it in
	// commit 4caa348).
	//
	// budgetPressureSnapshot avoids the grouping collapse (see its doc
	// comment), so its rendered content genuinely scales past the chosen
	// budget and must be trimmed row by row. At --budget 2048 this fixture's
	// natural (untrimmed) content is ~2165 bytes — bigger than the budget
	// even before the ~155-byte tail is reserved — so a correct Reserve
	// forces real truncation that keeps stdout within budget, while
	// Reserve:0 (Step 5's second sabotage) lets content through untrimmed and
	// overshoots once the tail is appended. That distinction is exactly what
	// this test must catch.
	writeSnapshot(t, dir, budgetPressureSnapshot(13, 2))
	var b bufs
	const budget = 2048
	if code := Run([]string{"map", "web", "--kind", "deployment", "--depth", "3", "--budget", strconv.Itoa(budget), "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	if b.out.Len() > budget {
		t.Fatalf("stdout is %d bytes, budget was %d:\n%s", b.out.Len(), budget, b.out.String())
	}
	if !strings.Contains(b.out.String(), "… truncated:") {
		t.Fatalf("this fixture at --budget %d should have needed truncation:\n%s", budget, b.out.String())
	}
}

func TestMap_BadArgumentsAreExit1(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	for _, args := range [][]string{
		{"map", "--data-dir", dir},                 // no name
		{"map", "web", "extra", "--data-dir", dir}, // two names
		{"map", "web", "--kind", "deployment", "--depth", "9", "--data-dir", dir},
		{"map", "web", "--kind", "deployment", "--budget", "10", "--data-dir", dir},
	} {
		var b bufs
		if code := Run(args, b.io()); code != ExitError {
			t.Errorf("%v: code = %d, want 1 (stderr %q)", args, code, b.err.String())
		}
		if b.out.Len() != 0 {
			t.Errorf("%v: stdout must stay empty on error", args)
		}
	}
}

// TestMap_BudgetMinimumNamesTheEffectiveFloor exercises the controller ruling
// from Task 6: kscope map always reserves bytes for its footer and counts
// line on top of graph.MinBudget, so --budget 1024 (the spec's documented
// minimum) can never actually succeed. cmd_map.go must catch this up front
// and name the real floor for this snapshot, rather than let it fall through
// to Neighbourhood's generic graph.ErrBudget.
func TestMap_BudgetMinimumNamesTheEffectiveFloor(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(2))

	var b bufs
	if code := Run([]string{"map", "web", "--kind", "deployment", "--budget", "1024", "--data-dir", dir}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want %d: stdout=%q stderr=%q", code, ExitError, b.out.String(), b.err.String())
	}
	if b.out.Len() != 0 {
		t.Fatalf("stdout must stay empty on error: %q", b.out.String())
	}
	e := b.err.String()
	if !strings.Contains(e, "kscope map: --budget must be at least") || !strings.Contains(e, "1024") {
		t.Fatalf("stderr must name the effective minimum, not just fail: %q", e)
	}

	// A budget comfortably above the effective floor must succeed.
	b = bufs{}
	if code := Run([]string{"map", "web", "--kind", "deployment", "--budget", "4096", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d, want %d: stderr=%q", code, ExitOK, b.err.String())
	}
	if b.out.Len() == 0 {
		t.Fatalf("expected a rendered map on stdout")
	}
}

// longChainSnapshot mirrors graph's longChainFixture (internal/graph/view_test.go):
// a five-deep chain built from unrealistically long names, so the "skeleton"
// (ancestors + focus + kubectl) alone exceeds --budget 4096. Namespace is kept
// short deliberately: it is the one name that also lands in the footer, and a
// long one would inflate Reserve enough to trip the earlier ErrBudget guard
// (budget-Reserve < MinBudget) before ErrSkeleton's own check ever runs — the
// CLI computes Reserve itself from the footer, so a naive "reuse the ns=63
// fixture at --budget 1024" does not reach ErrSkeleton through the CLI at
// all, only ErrBudget.
func longChainSnapshot() (graph.Snapshot, string, string) {
	longNS := strings.Repeat("n", 12)
	longDep := strings.Repeat("d", 2000)
	longRS := strings.Repeat("r", 2000)
	longPod := strings.Repeat("p", 2000)
	nsID := "core/namespace/" + longNS
	depID := "apps/deployment/" + longNS + "/" + longDep
	rsID := "apps/replicaset/" + longNS + "/" + longRS
	podID := "core/pod/" + longNS + "/" + longPod
	snap := graph.Snapshot{
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Scope:     graph.Scope{Context: "dev/ci1", Namespaces: []string{longNS}, IncludeInfra: true, IncludeCRDs: true},
		Cluster:   graph.ClusterMeta{Context: "dev/ci1"},
		Nodes: []graph.Node{
			{ID: "cluster", Kind: "Cluster", Name: "dev/ci1", Health: graph.HealthHealthy, Synthetic: true},
			{ID: nsID, Kind: "Namespace", Name: longNS, ParentID: "cluster", Health: graph.HealthHealthy},
			{ID: depID, Kind: "Deployment", Name: longDep, Namespace: longNS, ParentID: nsID, Health: graph.HealthHealthy},
			{ID: rsID, Kind: "ReplicaSet", Name: longRS, Namespace: longNS, ParentID: depID, Health: graph.HealthHealthy},
			{ID: podID, Kind: "Pod", Name: longPod, Namespace: longNS, ParentID: rsID, Health: graph.HealthHealthy,
				Kubectl: "kubectl --context dev/ci1 -n " + longNS + " get pod " + longPod + " -o yaml " + strings.Repeat("x", 2000)},
		},
	}
	return snap, longPod, longNS
}

// This is the ErrSkeleton addition (task-10-brief did not have this case; it
// predates ErrSkeleton). --budget 1024 itself is not reachable through the
// CLI for this: at Budget==MinBudget, any non-empty Reserve (the footer is
// never empty) already makes budget-Reserve<MinBudget, so Neighbourhood
// returns ErrBudget before its skeleton check ever runs — a strictly earlier
// guard than the one this test targets. --budget 4096 leaves enough headroom
// past that guard for the skeleton itself to be the thing that's too big.
func TestMap_SkeletonTooLargeForBudgetIsExit1(t *testing.T) {
	dir := t.TempDir()
	snap, longPod, longNS := longChainSnapshot()
	writeSnapshot(t, dir, snap)
	var b bufs
	if code := Run([]string{"map", longPod, "--namespace", longNS, "--budget", "4096", "--data-dir", dir}, b.io()); code != ExitError {
		t.Fatalf("code = %d, want %d: stdout=%q stderr=%q", code, ExitError, b.out.String(), b.err.String())
	}
	if b.out.Len() != 0 {
		t.Fatalf("stdout must stay empty on error: %q", b.out.String())
	}
	if !strings.Contains(b.err.String(), graph.ErrSkeleton.Error()) {
		t.Fatalf("stderr must name the error: %q", b.err.String())
	}
	// The up-front floor cannot predict this one, so the message has to say
	// what the caller can do about it rather than only what went wrong.
	if !strings.Contains(b.err.String(), "retry with a --budget above 4096") {
		t.Fatalf("stderr must name the fix: %q", b.err.String())
	}
}

func TestManifest_PrintsYAMLWithCommentFooter(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	var b bufs
	if code := Run([]string{"manifest", "web", "--kind", "deployment", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	out := b.out.String()
	if !strings.HasPrefix(out, "apiVersion: apps/v1\nkind: Deployment\n") {
		t.Fatalf("expected the stored YAML first:\n%s", out)
	}
	if !strings.HasSuffix(out, "# snapshot 4h12m old · context=dev/ci1 · ns=[app] · data="+dir+"\n") {
		t.Fatalf("footer must be a trailing YAML comment:\n%s", out)
	}
}

func TestManifest_SyntheticNodeIsExit2(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	var b bufs
	if code := Run([]string{"manifest", "dev/ci1", "--kind", "cluster", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d: %s", code, ExitMiss, b.err.String())
	}
	if b.out.Len() != 0 || !strings.Contains(b.err.String(), "synthetic") {
		t.Fatalf("stdout=%q stderr=%q", b.out.String(), b.err.String())
	}
}

func TestManifest_MissingManifestIsExit2(t *testing.T) {
	// The Service exists in the graph but has no captured manifest (a
	// pre-M2 snapshot, or a kind discovery does not capture).
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	var b bufs
	if code := Run([]string{"manifest", "web", "--kind", "service", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d: %s", code, ExitMiss, b.err.String())
	}
	if !strings.Contains(b.err.String(), "no manifest") || !strings.Contains(b.err.String(), "--discover-namespaces=app") {
		t.Fatalf("stderr must explain and name the refresh: %q", b.err.String())
	}
}

// A --namespace value is interpolated into the refresh command a miss
// message prints for an agent to run, so anything that is not a namespace
// must be rejected where the flag is read — before the store is touched, the
// way --limit and --health already are. 'x --context prod' is the sharp case:
// Go's flag package takes the last occurrence, so the "refresh" would
// discover a different cluster and overwrite the snapshot with it.
func TestNamespaceMustBeADNS1123Label(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, findSnapshot())
	empty := t.TempDir() // no snapshot: a late check would be exit 3, not 1

	bad := []string{"a b", "x --context prod", "$(id)", "a;b", "UPPER", strings.Repeat("n", 64)}
	for _, ns := range bad {
		for _, args := range [][]string{
			{"map", "web", "--namespace", ns},
			{"find", "--namespace", ns},
			{"manifest", "web", "--namespace", ns},
		} {
			for _, d := range []string{dir, empty} {
				var b bufs
				if code := Run(append(args, "--data-dir", d), b.io()); code != ExitError {
					t.Errorf("%v ns=%q: code = %d, want %d (stderr %q)", args, ns, code, ExitError, b.err.String())
				}
				if b.out.Len() != 0 {
					t.Errorf("%v ns=%q: stdout must stay empty, got %q", args, ns, b.out.String())
				}
				if !strings.Contains(b.err.String(), "--namespace") {
					t.Errorf("%v ns=%q: stderr must name the flag, got %q", args, ns, b.err.String())
				}
			}
		}
	}

	// A real namespace still works, and the empty value still means "no filter".
	for _, args := range [][]string{
		{"find", "--namespace", "app", "--data-dir", dir},
		{"find", "--data-dir", dir},
	} {
		var b bufs
		if code := Run(args, b.io()); code != ExitOK {
			t.Errorf("%v: code = %d, want 0 (stderr %q)", args, code, b.err.String())
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"dev/ci1":              "dev/ci1",
		"/home/u/.local/share": "/home/u/.local/share",
		"app,cube":             "app,cube",
		"/home/my data/kscope": `'/home/my data/kscope'`,
		"$(id)":                `'$(id)'`,
		"it's":                 `'it'\''s'`,
		"a;rm -rf /":           `'a;rm -rf /'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// Anything the hint or the no-snapshot message interpolates has to survive as
// one shell word: a data dir with a space would otherwise end the --data-dir
// flag and turn the rest of the line into positionals, which stops flag
// parsing and silently drops the scope flags that follow.
func TestDataDirWithASpaceIsQuotedInBothCommands(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := filepath.Join(t.TempDir(), "my data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Exit 3: no snapshot yet. The message names the command that fills it.
	var b bufs
	if code := Run([]string{"info", "--data-dir", dir}, b.io()); code != ExitNoSnapshot {
		t.Fatalf("code = %d, want %d", code, ExitNoSnapshot)
	}
	if n := strings.Count(b.err.String(), "'"+dir+"'"); n != 2 {
		t.Fatalf("both interpolations of the data dir must be quoted, found %d in:\n%s", n, b.err.String())
	}

	// Exit 2: a miss, whose hint is the refresh command.
	writeSnapshot(t, dir, findSnapshot())
	b = bufs{}
	if code := Run([]string{"find", "--name-contains", "cube", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d", code, ExitMiss)
	}
	if !strings.Contains(b.err.String(), "--data-dir '"+dir+"'") {
		t.Fatalf("the refresh hint must quote the data dir:\n%s", b.err.String())
	}
}

// A snapshot with neither Cluster.Context nor Scope.Context (written before
// ClusterMeta existed, or by hand) must not yield "kscope --context
// --data-dir /path": flag would bind "--data-dir" as the context value, /path
// would become a positional that stops parsing, and the command an agent was
// told to run would start the HTTP server and block.
func TestRefreshHint_OmitsAnUnknownContext(t *testing.T) {
	snap := findSnapshot()
	snap.Scope.Context, snap.Cluster.Context = "", ""
	hint := refreshHint(snap, "/d", "cube")
	if strings.Contains(hint, "--context") {
		t.Fatalf("an unknown context must not be emitted as a flag: %q", hint)
	}
	if !strings.Contains(hint, "# the snapshot records no context") {
		t.Fatalf("the hint must explain what it will discover instead: %q", hint)
	}
	if !strings.Contains(hint, "kscope --data-dir /d \\\n") || !strings.Contains(hint, "--discover-namespaces=app,cube") {
		t.Fatalf("the rest of the command must be intact: %q", hint)
	}
}

// find validates --limit and --health before it reads the store; map must
// report a bad --budget or --depth the same way, rather than resolving the
// name first and answering an also-invalid invocation with a miss.
func TestMap_ArgumentErrorsPrecedeResolution(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(1))
	cases := []struct {
		args []string
		flag string
	}{
		{[]string{"map", "ghost", "--budget", "10", "--data-dir", dir}, "--budget"},
		{[]string{"map", "ghost", "--depth", "9", "--data-dir", dir}, "--depth"},
		{[]string{"map", "ghost", "--depth", "-1", "--data-dir", dir}, "--depth"},
	}
	for _, c := range cases {
		var b bufs
		if code := Run(c.args, b.io()); code != ExitError {
			t.Errorf("%v: code = %d, want %d (the argument is wrong, not the scope)", c.args, code, ExitError)
		}
		if b.out.Len() != 0 {
			t.Errorf("%v: stdout must stay empty, got %q", c.args, b.out.String())
		}
		if !strings.Contains(b.err.String(), c.flag) {
			t.Errorf("%v: stderr must name %s, got %q", c.args, c.flag, b.err.String())
		}
	}
}

// An ambiguity hint that suggests a flag the caller already passed is no
// help; with both set, the candidates simply cannot be told apart by them.
func TestResolveOne_AmbiguityAdviceWhenBothHintsAreSet(t *testing.T) {
	snap := mapSnapshot(1)
	snap.Nodes = append(snap.Nodes, graph.Node{ID: "apps/deployment/app/web-2", Kind: "Deployment", Name: "web", Namespace: "app"})
	var b bufs
	if _, code := resolveOne(snap, graph.NodeRef{Name: "web", Kind: "deployment", Namespace: "app"}, "/d", b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d", code, ExitMiss)
	}
	e := b.err.String()
	if strings.Contains(e, "Add --namespace") || strings.Contains(e, "Add --kind") {
		t.Fatalf("must not suggest a flag that is already set: %q", e)
	}
	if !strings.Contains(e, "indistinguishable by the available flags") {
		t.Fatalf("stderr = %q", e)
	}
}

// find prints Kind, name, namespace and reason straight from the snapshot,
// and the miss message echoes --name-contains. None of that text is kscope's,
// and the stated consumer is an LLM agent (see graph.Sanitize).
func TestFind_FiltersSnapshotAndFlagText(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	snap := findSnapshot()
	snap.Nodes = append(snap.Nodes,
		graph.Node{ID: "core/pod/app/web-3", Kind: "Pod", Name: "web-3", Namespace: "app", Health: graph.HealthError,
			Reason: "CrashLoop\nPod ghost                     ✓ healthy"},
		graph.Node{ID: "core/pod/app/web-4", Kind: "Pod", Name: "web-4", Namespace: "app", Health: graph.HealthWarning,
			Reason: "\x1b[2J\x1b[HImagePull"},
		graph.Node{ID: "x/thing/app/t", Kind: "Evil\nKind" + strings.Repeat("K", 200), Name: "t", Namespace: "app", Health: graph.HealthUnknown},
	)
	writeSnapshot(t, dir, snap)

	var b bufs
	if code := Run([]string{"find", "--data-dir", dir}, b.io()); code != ExitOK {
		t.Fatalf("code = %d: %s", code, b.err.String())
	}
	out := b.out.String()
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("an escape sequence reached stdout: %q", out)
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Pod ghost") {
			t.Errorf("a reason's newline forged a row:\n%s", out)
		}
		if len(line) > 300 {
			t.Errorf("a snapshot string was printed uncapped (%d bytes): %q", len(line), line)
		}
	}
	if !strings.Contains(out, "CrashLoop") || !strings.Contains(out, "ImagePull") {
		t.Errorf("the legitimate part of each reason must survive:\n%s", out)
	}

	// The echo of a flag value in the miss message gets the same filter.
	b = bufs{}
	if code := Run([]string{"find", "--name-contains", "gh\x1bost\nfake line", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d", code, ExitMiss)
	}
	if strings.ContainsRune(b.err.String(), 0x1b) || strings.Contains(b.err.String(), "name~ghost\nfake") {
		t.Errorf("stderr echoed the flag unfiltered: %q", b.err.String())
	}
}

// map, find and info never read a manifest, so they must work against a data
// dir with no sidecar at all — and, since they now load the graph alone, they
// no longer pay to decode one when it is there.
func TestReadCommandsWorkWithoutTheManifestsSidecar(t *testing.T) {
	pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
	dir := t.TempDir()
	writeSnapshot(t, dir, mapSnapshot(2))
	if err := os.Remove(filepath.Join(dir, "manifests.json")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"info", "--data-dir", dir},
		{"find", "--kind", "pod", "--data-dir", dir},
		{"map", "web", "--kind", "deployment", "--data-dir", dir},
	} {
		var b bufs
		if code := Run(args, b.io()); code != ExitOK {
			t.Errorf("%v: code = %d (stderr %q)", args, code, b.err.String())
		}
		if b.out.Len() == 0 {
			t.Errorf("%v: expected output on stdout", args)
		}
	}
	// manifest is the one that needs the sidecar, and says so recoverably.
	var b bufs
	if code := Run([]string{"manifest", "web", "--kind", "deployment", "--data-dir", dir}, b.io()); code != ExitMiss {
		t.Fatalf("code = %d, want %d (stderr %q)", code, ExitMiss, b.err.String())
	}
	if !strings.Contains(b.err.String(), "no manifest") {
		t.Fatalf("stderr = %q", b.err.String())
	}
}

// A real cluster running Crossplane and Kyverno put 137 kinds in one snapshot,
// which the old code rendered as a single 2,750-character line — the opposite of
// what a cheap orientation command should cost an agent. The line-count budget
// in TestInfo_SummarisesErrorsInsteadOfListingThem never caught it, because its
// fixture has two kinds.
func TestInfo_CapsTheKindsLine(t *testing.T) {
	// A real cluster running Crossplane and Kyverno put 137 kinds in one
	// snapshot, which the old code rendered as a single 2,750-character line —
	// the opposite of what a cheap orientation command should cost an agent. The
	// line-count budget in TestInfo_SummarisesErrorsInsteadOfListingThem never
	// caught it, because its fixture has two kinds.
	//
	// The cap is a byte budget, not a count, so the cases below use the kind
	// names that actually broke a count-based cap: twelve Crossplane kinds are
	// one line of Pods and Deployments but 333 characters of these.
	long := []string{
		"ManagedResourceDefinition", "CompositeResourceDefinition", "CompositionRevision",
		"ProviderConfigUsage", "ClusterPolicyReport", "DeploymentRuntimeConfig",
		"ClusterAdmissionReport", "RolePolicyAttachment", "CiliumIdentity",
		"VPCEndpointSubnetAssociation", "ManagedResourceActivationPolicy", "ProviderRevision",
	}
	cases := []struct {
		name  string
		kinds func() map[string]int
	}{
		{"many short kinds", func() map[string]int {
			m := map[string]int{}
			for i := 0; i < 137; i++ {
				m[fmt.Sprintf("Kind%03d", i)] = 500 - i
			}
			return m
		}},
		{"realistically long kind names", func() map[string]int {
			m := map[string]int{}
			for i, k := range long {
				m[k] = 500 - i
			}
			for i := 0; i < 90; i++ {
				m[fmt.Sprintf("Extra%03d", i)] = 100 - i
			}
			return m
		}},
		{"one kind whose name alone exceeds the budget", func() map[string]int {
			return map[string]int{strings.Repeat("A", kindsBudget+50): 7, "Pod": 3}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinClock(t, time.Date(2026, 9, 14, 10, 12, 0, 0, time.UTC))
			dir := t.TempDir()
			snap := infoSnapshot()
			counts := tc.kinds()
			snap.Stats.Counts = counts
			writeSnapshot(t, dir, snap)

			var b bufs
			if code := Run([]string{"info", "--data-dir", dir}, b.io()); code != ExitOK {
				t.Fatalf("code = %d: %s", code, b.err.String())
			}
			var kindsLine string
			for _, l := range strings.Split(b.out.String(), "\n") {
				if strings.HasPrefix(l, "kinds ") {
					kindsLine = l
				}
			}
			if kindsLine == "" {
				t.Fatalf("no kinds line:\n%s", b.out.String())
			}
			// "kinds     " is the label; the budget governs the list itself.
			list := strings.TrimPrefix(kindsLine, "kinds     ")
			if len(list) > kindsBudget && !strings.HasPrefix(list, strings.Repeat("A", 10)) {
				t.Fatalf("list is %d bytes, budget is %d:\n%s", len(list), kindsBudget, list)
			}
			// At least one kind is always named, and every node is accounted
			// for — either named or counted in the tail.
			// Kind names are rendered through graph.Sanitize, which caps them at
			// ShortText — so match the rendered form, not the raw key.
			rendered := func(k string) string { return graph.Sanitize(k, graph.ShortText) }
			named := 0
			for k := range counts {
				if strings.Contains(list, rendered(k)+" ") {
					named++
				}
			}
			if named == 0 {
				t.Fatalf("no kind named: %q", list)
			}
			total := 0
			for _, n := range counts {
				total += n
			}
			accounted := 0
			for k, n := range counts {
				if strings.Contains(list, rendered(k)+" "+strconv.Itoa(n)) {
					accounted += n
				}
			}
			if m := regexp.MustCompile(`\+(\d+) more kinds \((\d+) nodes\)`).FindStringSubmatch(list); m != nil {
				tail, _ := strconv.Atoi(m[2])
				accounted += tail
			}
			if accounted != total {
				t.Fatalf("accounted for %d of %d nodes:\n%s", accounted, total, list)
			}
		})
	}
}
