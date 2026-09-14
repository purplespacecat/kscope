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

// fleetFixture: a Deployment with five Pods in mixed health, so the pods
// group; two ConfigMaps (below the threshold); three Deployments with
// ReplicaSets (non-leaf, so never grouped); three Namespaces (exempt).
func fleetFixture() *fixture {
	f := &fixture{}
	f.add(Node{ID: fxCluster, Kind: "Cluster", Name: "dev/ci1"})
	for _, ns := range []string{"app", "ops", "web"} {
		f.add(Node{ID: "core/namespace/" + ns, Kind: "Namespace", Name: ns, ParentID: fxCluster})
	}
	f.add(Node{ID: fxDeploy, Kind: "Deployment", Name: "web", Namespace: "app", ParentID: fxNS})
	f.add(Node{ID: fxRS, Kind: "ReplicaSet", Name: "web-abc", Namespace: "app", ParentID: fxDeploy})
	f.add(Node{ID: "core/pod/app/web-abc-err", Kind: "Pod", Name: "web-abc-err", Namespace: "app", ParentID: fxRS, Health: HealthError, Reason: "CrashLoopBackOff"})
	f.add(Node{ID: "core/pod/app/web-abc-warn", Kind: "Pod", Name: "web-abc-warn", Namespace: "app", ParentID: fxRS, Health: HealthWarning, Reason: "ImagePullBackOff"})
	for _, s := range []string{"a", "b", "c"} {
		f.add(Node{ID: "core/pod/app/web-abc-" + s, Kind: "Pod", Name: "web-abc-" + s, Namespace: "app", ParentID: fxRS})
	}
	for _, c := range []string{"one", "two"} {
		f.add(Node{ID: "core/configmap/app/" + c, Kind: "ConfigMap", Name: c, Namespace: "app", ParentID: fxNS})
	}
	// Three Deployments under ns ops, each owning a ReplicaSet: non-leaf.
	for _, d := range []string{"d1", "d2", "d3"} {
		f.add(Node{ID: "apps/deployment/ops/" + d, Kind: "Deployment", Name: d, Namespace: "ops", ParentID: "core/namespace/ops"})
		f.add(Node{ID: "apps/replicaset/ops/" + d + "-rs", Kind: "ReplicaSet", Name: d + "-rs", Namespace: "ops", ParentID: "apps/deployment/ops/" + d})
	}
	return f
}

func TestNeighbourhood_GroupsLeafSiblings(t *testing.T) {
	text := render(t, fleetFixture(), fxDeploy, ViewOptions{Depth: 2})
	header := lineWith(t, text, "Pods (5)")
	if !strings.Contains(header, "✓3 !1 ✗1") {
		t.Fatalf("rollup missing or wrong: %q", header)
	}
	if strings.Contains(header, "?") {
		t.Fatalf("zero counts must be omitted from the rollup: %q", header)
	}
	lineWith(t, text, "web-abc-err", "✗ CrashLoopBackOff")
	lineWith(t, text, "web-abc-warn", "! ImagePullBackOff")
	lineWith(t, text, "… +2 more")
	// Unhealthy members come first, then one healthy example, then "more".
	errAt := strings.Index(text, "web-abc-err")
	warnAt := strings.Index(text, "web-abc-warn")
	aAt := strings.Index(text, "web-abc-a")
	moreAt := strings.Index(text, "… +2 more")
	if !(errAt < warnAt && warnAt < aAt && aAt < moreAt) {
		t.Fatalf("member order wrong:\n%s", text)
	}
	// Only three members are listed.
	if strings.Contains(text, "web-abc-b") || strings.Contains(text, "web-abc-c") {
		t.Fatalf("more than three members listed:\n%s", text)
	}
	// Members carry the name only; the kind is in the header.
	if strings.Contains(text, "Pod web-abc-err") {
		t.Fatalf("group members must not repeat the kind:\n%s", text)
	}
}

func TestNeighbourhood_GroupingExemptions(t *testing.T) {
	f := fleetFixture()

	// Two ConfigMaps: below the threshold, listed individually.
	ns := render(t, f, fxNS, ViewOptions{Depth: 1})
	lineWith(t, ns, "ConfigMap one")
	lineWith(t, ns, "ConfigMap two")
	if strings.Contains(ns, "ConfigMaps (") {
		t.Fatalf("two siblings must not group:\n%s", ns)
	}

	// Three Namespaces under the cluster: exempt.
	cl := render(t, f, fxCluster, ViewOptions{Depth: 1})
	for _, n := range []string{"ns app", "ns ops", "ns web"} {
		lineWith(t, cl, n)
	}
	if strings.Contains(cl, "Namespaces (") {
		t.Fatalf("namespaces must never group:\n%s", cl)
	}

	// Three Deployments that own ReplicaSets are non-leaf: never grouped,
	// even at depth 1 where their children are not shown.
	ops := render(t, f, "core/namespace/ops", ViewOptions{Depth: 1})
	for _, d := range []string{"Deployment d1", "Deployment d2", "Deployment d3"} {
		lineWith(t, ops, d)
	}
	if strings.Contains(ops, "Deployments (") {
		t.Fatalf("non-leaf siblings must not group:\n%s", ops)
	}
}

func TestNeighbourhood_GroupHeaderUsesPlural(t *testing.T) {
	// "Ingress" → "Ingresses", via the same pluralize the k9s handoff uses.
	f := &fixture{}
	f.add(Node{ID: fxCluster, Kind: "Cluster", Name: "c"})
	f.add(Node{ID: fxNS, Kind: "Namespace", Name: "app", ParentID: fxCluster})
	for _, n := range []string{"a", "b", "c"} {
		f.add(Node{ID: "networking.k8s.io/ingress/app/" + n, Kind: "Ingress", Name: n, Namespace: "app", ParentID: fxNS})
	}
	text := render(t, f, fxNS, ViewOptions{Depth: 1})
	lineWith(t, text, "Ingresses (3)")
}

// TestNeighbourhood_NamespacesNeverGroupEvenAsLeaves uses its own fixture of
// three truly empty namespaces (no children anywhere) so the Namespace
// exclusion in build is the only thing stopping the group — unlike
// fleetFixture, where ns app/ns ops already own children and so are excluded
// by allLeaves regardless of kind.
func TestNeighbourhood_NamespacesNeverGroupEvenAsLeaves(t *testing.T) {
	f := &fixture{}
	f.add(Node{ID: fxCluster, Kind: "Cluster", Name: "dev/ci1"})
	for _, ns := range []string{"a", "b", "c"} {
		f.add(Node{ID: "core/namespace/" + ns, Kind: "Namespace", Name: ns, ParentID: fxCluster})
	}
	text := render(t, f, fxCluster, ViewOptions{Depth: 1})
	for _, n := range []string{"ns a", "ns b", "ns c"} {
		lineWith(t, text, n)
	}
	if strings.Contains(text, "Namespaces (") {
		t.Fatalf("namespaces must never group, even when they are leaves:\n%s", text)
	}
}

// wired adds the Secret every pod in fleetFixture mounts, plus a Service
// selecting them and a Kustomization managing the Service — the second hop
// that must NOT appear.
func wiredFleet() *fixture {
	f := fleetFixture()
	f.add(Node{ID: fxSecret, Kind: "Secret", Name: "db-creds", Namespace: "app", ParentID: fxNS})
	f.add(Node{ID: fxSvc, Kind: "Service", Name: "web", Namespace: "app", ParentID: fxNS})
	f.add(Node{ID: "kustomize.toolkit.fluxcd.io/kustomization/flux-system/infra", Kind: "Kustomization", Name: "infra", Namespace: "flux-system", ParentID: fxNS})
	for _, p := range []string{"err", "warn", "a", "b", "c"} {
		f.edge(EdgeMounts, "core/pod/app/web-abc-"+p, fxSecret)
		f.edge(EdgeSelects, fxSvc, "core/pod/app/web-abc-"+p)
	}
	f.edge(EdgeManagedBy, fxSvc, "kustomize.toolkit.fluxcd.io/kustomization/flux-system/infra")
	return f
}

func TestNeighbourhood_EdgesBothDirections(t *testing.T) {
	// Two ungrouped pods: each renders its own edges, so both directions
	// appear once per pod.
	text := render(t, deployFixture(), fxDeploy, ViewOptions{Depth: 2})
	if n := strings.Count(text, "mounts → Secret db-creds"); n != 2 {
		t.Fatalf("expected the mounts edge under each of 2 pods, got %d:\n%s", n, text)
	}
	if n := strings.Count(text, "uses → ServiceAccount runner"); n != 2 {
		t.Fatalf("expected the uses edge under each of 2 pods, got %d:\n%s", n, text)
	}
	if n := strings.Count(text, "← selects Service web"); n != 2 {
		t.Fatalf("expected the incoming selects edge under each of 2 pods, got %d:\n%s", n, text)
	}
	if strings.Contains(text, "selected by") {
		t.Fatalf("no inverse labels — the arrow carries direction:\n%s", text)
	}
}

func TestNeighbourhood_GroupEdgesAreAUnion(t *testing.T) {
	text := render(t, wiredFleet(), fxDeploy, ViewOptions{Depth: 2})
	if n := strings.Count(text, "mounts → Secret db-creds"); n != 1 {
		t.Fatalf("five pods mounting one Secret must render once under the group, got %d:\n%s", n, text)
	}
	if n := strings.Count(text, "← selects Service web"); n != 1 {
		t.Fatalf("incoming edges are unioned too, got %d:\n%s", n, text)
	}
	// Edges hang under the group, after its members and the "more" row.
	more := strings.Index(text, "… +2 more")
	mounts := strings.Index(text, "mounts → Secret")
	if !(more < mounts) {
		t.Fatalf("group edges must follow the member rows:\n%s", text)
	}
}

func TestNeighbourhood_EdgesAreOneHop(t *testing.T) {
	// The Service is reached as a peer of the pods; its own managed-by edge
	// is a second hop and stays out.
	text := render(t, wiredFleet(), fxDeploy, ViewOptions{Depth: 2})
	lineWith(t, text, "← selects Service web")
	if strings.Contains(text, "managed-by") || strings.Contains(text, "Kustomization infra") {
		t.Fatalf("second hop leaked:\n%s", text)
	}
}

func TestNeighbourhood_EdgesRespectDepth(t *testing.T) {
	// At depth 1 the pods are not in the set, so their wiring is not either;
	// the Deployment itself has no edges, so no arrows at all.
	text := render(t, deployFixture(), fxDeploy, ViewOptions{Depth: 1})
	if strings.Contains(text, "→") || strings.Contains(text, "←") {
		t.Fatalf("depth 1 must not pull in the pods' edges:\n%s", text)
	}
}

func TestNeighbourhood_FocusEdgesFollowDescendants(t *testing.T) {
	text := render(t, deployFixture(), fxPod1, ViewOptions{Depth: 1})
	lineWith(t, text, "mounts → Secret db-creds")
	lineWith(t, text, "uses → ServiceAccount runner")
	lineWith(t, text, "← selects Service web")
	// Outgoing before incoming.
	if strings.Index(text, "mounts →") > strings.Index(text, "← selects") {
		t.Fatalf("outgoing edges must precede incoming:\n%s", text)
	}
}
