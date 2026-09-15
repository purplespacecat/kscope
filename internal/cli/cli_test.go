package cli

import (
	"bytes"
	"path/filepath"
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
