package graph

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fixture builds snapshots for projection tests without a cluster or a
// fake clientset: containment is ParentID, wiring is Edges, and every ID is
// spelled out so assertions can point at exact lines.
type fixture struct {
	nodes []Node
	edges []Edge
}

func (f *fixture) add(n Node) *fixture {
	if n.Health == "" {
		n.Health = HealthHealthy
	}
	f.nodes = append(f.nodes, n)
	return f
}

func (f *fixture) edge(kind, src, dst string) *fixture {
	f.edges = append(f.edges, Edge{ID: src + " -" + kind + "-> " + dst, Kind: kind, Source: src, Target: dst})
	return f
}

func (f *fixture) snap() Snapshot {
	return Snapshot{
		Scope:     Scope{Context: "dev/ci1", Namespaces: []string{"app"}},
		Timestamp: time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC),
		Cluster:   ClusterMeta{Context: "dev/ci1", Version: "v1.34.9"},
		Nodes:     f.nodes,
		Edges:     f.edges,
	}
}

const (
	fxCluster = "cluster"
	fxNS      = "core/namespace/app"
	fxDeploy  = "apps/deployment/app/web"
	fxRS      = "apps/replicaset/app/web-abc"
	fxPod1    = "core/pod/app/web-abc-1"
	fxPod2    = "core/pod/app/web-abc-2"
	fxSecret  = "core/secret/app/db-creds"
	fxSA      = "core/serviceaccount/app/runner"
	fxSvc     = "core/service/app/web"
)

// deployFixture is the canonical chain: Cluster → ns app → Deployment web →
// ReplicaSet web-abc → two Pods (one crash-looping), plus the Secret, SA and
// Service the pods are wired to. Two pods, so nothing groups (see Task 4).
func deployFixture() *fixture {
	f := &fixture{}
	f.add(Node{ID: fxCluster, Kind: "Cluster", Name: "dev/ci1", Kubectl: "kubectl --context dev/ci1 cluster-info"})
	f.add(Node{ID: fxNS, Kind: "Namespace", Name: "app", ParentID: fxCluster})
	f.add(Node{ID: fxDeploy, Kind: "Deployment", Name: "web", Namespace: "app", ParentID: fxNS,
		Kubectl: "kubectl --context dev/ci1 -n app get deployment web -o yaml",
		GitOps:  &GitOpsRef{Tool: "flux", Kind: "Kustomization", Name: "infra", Namespace: "flux-system"}})
	f.add(Node{ID: fxRS, Kind: "ReplicaSet", Name: "web-abc", Namespace: "app", ParentID: fxDeploy,
		Kubectl: "kubectl --context dev/ci1 -n app get replicaset web-abc -o yaml"})
	f.add(Node{ID: fxPod1, Kind: "Pod", Name: "web-abc-1", Namespace: "app", ParentID: fxRS,
		Kubectl: "kubectl --context dev/ci1 -n app get pod web-abc-1 -o yaml"})
	f.add(Node{ID: fxPod2, Kind: "Pod", Name: "web-abc-2", Namespace: "app", ParentID: fxRS,
		Health: HealthError, Reason: "CrashLoopBackOff",
		Kubectl: "kubectl --context dev/ci1 -n app get pod web-abc-2 -o yaml"})
	f.add(Node{ID: fxSecret, Kind: "Secret", Name: "db-creds", Namespace: "app", ParentID: fxNS})
	f.add(Node{ID: fxSA, Kind: "ServiceAccount", Name: "runner", Namespace: "app", ParentID: fxNS})
	f.add(Node{ID: fxSvc, Kind: "Service", Name: "web", Namespace: "app", ParentID: fxNS})
	f.edge(EdgeMounts, fxPod1, fxSecret).edge(EdgeMounts, fxPod2, fxSecret)
	f.edge(EdgeUses, fxPod1, fxSA).edge(EdgeUses, fxPod2, fxSA)
	f.edge(EdgeSelects, fxSvc, fxPod1).edge(EdgeSelects, fxSvc, fxPod2)
	return f
}

func render(t *testing.T, f *fixture, focus string, opts ViewOptions) string {
	t.Helper()
	r, err := Neighbourhood(f.snap(), focus, opts)
	if err != nil {
		t.Fatalf("Neighbourhood: %v", err)
	}
	return r.Text
}

// lineWith returns the first output line containing every fragment, or
// fails — assertions name the line they mean instead of substring-matching
// the whole blob.
func lineWith(t *testing.T, text string, fragments ...string) string {
	t.Helper()
outer:
	for _, l := range strings.Split(text, "\n") {
		for _, fr := range fragments {
			if !strings.Contains(l, fr) {
				continue outer
			}
		}
		return l
	}
	t.Fatalf("no line containing %q in:\n%s", fragments, text)
	return ""
}

func TestNeighbourhood_AncestorChainIsAlwaysComplete(t *testing.T) {
	// Deepest node, shallowest depth: every ancestor still appears, because
	// orientation is not what --depth trades away.
	text := render(t, deployFixture(), fxPod2, ViewOptions{Depth: 1})
	for _, want := range []string{"Cluster dev/ci1", "ns app", "Deployment web", "ReplicaSet web-abc", "Pod web-abc-2"} {
		lineWith(t, text, want)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "Cluster") || !strings.HasPrefix(lines[1], "└─ ns app") || !strings.HasPrefix(lines[2], "   └─ Deployment web") ||
		!strings.HasPrefix(lines[4], "         └─ Pod web-abc-2") {
		t.Fatalf("chain not nested as expected:\n%s", text)
	}
}

func TestNeighbourhood_DepthBoundsDescendants(t *testing.T) {
	f := deployFixture()

	d1 := render(t, f, fxDeploy, ViewOptions{Depth: 1})
	lineWith(t, d1, "ReplicaSet web-abc")
	if strings.Contains(d1, "Pod web-abc") {
		t.Fatalf("depth 1 must stop at the ReplicaSet:\n%s", d1)
	}

	d2 := render(t, f, fxDeploy, ViewOptions{Depth: 2})
	lineWith(t, d2, "Pod web-abc-1")
	lineWith(t, d2, "Pod web-abc-2")

	// Depth 0 means the default, which is 2 — the Deployment→RS→Pods query.
	def := render(t, f, fxDeploy, ViewOptions{})
	lineWith(t, def, "Pod web-abc-1")
}

func TestNeighbourhood_HealthAndReason(t *testing.T) {
	text := render(t, deployFixture(), fxDeploy, ViewOptions{})
	lineWith(t, text, "Deployment web", "✓ healthy")
	lineWith(t, text, "Pod web-abc-2", "✗ CrashLoopBackOff")
	if l := lineWith(t, text, "Pod web-abc-1"); !strings.HasSuffix(strings.TrimRight(l, " "), "✓") {
		t.Fatalf("healthy pod should end in a bare glyph: %q", l)
	}
}

func TestNeighbourhood_FocusExtrasOnlyOnFocus(t *testing.T) {
	text := render(t, deployFixture(), fxDeploy, ViewOptions{})
	lineWith(t, text, "Deployment web", "[flux: Kustomization/infra]")
	lineWith(t, text, "kubectl --context dev/ci1 -n app get deployment web -o yaml")
	// Every fixture node has a kubectl string; only the focus prints it.
	if n := strings.Count(text, "kubectl "); n != 1 {
		t.Fatalf("expected exactly one kubectl line, got %d:\n%s", n, text)
	}
}

func TestNeighbourhood_NeverPrintsIDs(t *testing.T) {
	text := render(t, deployFixture(), fxDeploy, ViewOptions{})
	for _, id := range []string{fxDeploy, fxRS, fxPod1, "core/", "apps/"} {
		if strings.Contains(text, id) {
			t.Fatalf("node ID %q leaked into output:\n%s", id, text)
		}
	}
}

func TestNeighbourhood_Errors(t *testing.T) {
	f := deployFixture()
	if _, err := Neighbourhood(f.snap(), "nope", ViewOptions{}); !errors.Is(err, ErrNoFocus) {
		t.Fatalf("unknown focus: err = %v, want ErrNoFocus", err)
	}
	if _, err := Neighbourhood(f.snap(), fxDeploy, ViewOptions{Depth: 4}); !errors.Is(err, ErrDepth) {
		t.Fatalf("depth 4: err = %v, want ErrDepth", err)
	}
	if _, err := Neighbourhood(f.snap(), fxDeploy, ViewOptions{Budget: 1023}); !errors.Is(err, ErrBudget) {
		t.Fatalf("budget 1023: err = %v, want ErrBudget", err)
	}
}
