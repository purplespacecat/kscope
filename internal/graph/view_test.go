package graph

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// longChainFixture: a five-deep chain (Cluster → ns → Deployment →
// ReplicaSet → Pod) built from unrealistically long names, so the
// "skeleton" (ancestors + focus + kubectl) alone can exceed even a generous
// budget. The focus (the Pod) carries a GitOps ref and a long Kubectl
// string, both of which only ever render on the focus line.
func longChainFixture() *fixture {
	longNS := strings.Repeat("n", 63)
	longDep := strings.Repeat("d", 250)
	longRS := strings.Repeat("r", 250)
	longPod := strings.Repeat("p", 250)
	nsID := "core/namespace/" + longNS
	depID := "apps/deployment/" + longNS + "/" + longDep
	rsID := "apps/replicaset/" + longNS + "/" + longRS
	podID := "core/pod/" + longNS + "/" + longPod

	f := &fixture{}
	f.add(Node{ID: fxCluster, Kind: "Cluster", Name: "dev/ci1"})
	f.add(Node{ID: nsID, Kind: "Namespace", Name: longNS, ParentID: fxCluster})
	f.add(Node{ID: depID, Kind: "Deployment", Name: longDep, Namespace: longNS, ParentID: nsID})
	f.add(Node{ID: rsID, Kind: "ReplicaSet", Name: longRS, Namespace: longNS, ParentID: depID})
	f.add(Node{ID: podID, Kind: "Pod", Name: longPod, Namespace: longNS, ParentID: rsID,
		Kubectl: "kubectl --context dev/ci1 -n " + longNS + " get pod " + longPod + " -o yaml " + strings.Repeat("x", 300),
		GitOps:  &GitOpsRef{Tool: "flux", Kind: "Kustomization", Name: "infra", Namespace: "flux-system"}})
	return f
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
	if _, err := Neighbourhood(f.snap(), fxDeploy, ViewOptions{Reserve: -1}); !errors.Is(err, ErrBudget) {
		t.Fatalf("negative reserve: err = %v, want ErrBudget", err)
	}
	if _, err := Neighbourhood(f.snap(), fxDeploy, ViewOptions{Budget: MinBudget, Reserve: 600}); !errors.Is(err, ErrBudget) {
		t.Fatalf("reserve leaves less than MinBudget: err = %v, want ErrBudget", err)
	}

	long := longChainFixture()
	podID := "core/pod/" + strings.Repeat("n", 63) + "/" + strings.Repeat("p", 250)
	if _, err := Neighbourhood(long.snap(), podID, ViewOptions{Budget: 1 << 20}); err != nil {
		t.Fatalf("long chain at a generous budget: unexpected err = %v", err)
	}
	if _, err := Neighbourhood(long.snap(), podID, ViewOptions{Budget: MinBudget, Reserve: 0}); !errors.Is(err, ErrSkeleton) {
		t.Fatalf("long chain at MinBudget: err = %v, want ErrSkeleton", err)
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
	// The Secret is reached from the pods via outgoing mounts; its own
	// managed-by edge is a second hop on the outgoing side and must never
	// render, mirroring the Service's trap on the incoming side.
	f.edge(EdgeManagedBy, fxSecret, "kustomize.toolkit.fluxcd.io/kustomization/flux-system/infra")
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
	// The Service (incoming) and the Secret (outgoing) are both reached as
	// peers of the pods; each has its own managed-by edge, which is a second
	// hop and must stay out regardless of which direction reached the peer.
	text := render(t, wiredFleet(), fxDeploy, ViewOptions{Depth: 2})
	lineWith(t, text, "← selects Service web")
	// Guard against the trap going vacuous: the outgoing-reached peer must
	// actually be present, or the absence check below proves nothing.
	lineWith(t, text, "mounts → Secret db-creds")
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

// wideFixture: one namespace with 30 Deployments, each owning a ReplicaSet
// with three Pods that mount a Secret. Alphabetical names so the total
// order is predictable; one Deployment is unhealthy and must outlive the
// healthy ones under pressure.
func wideFixture() *fixture {
	f := &fixture{}
	f.add(Node{ID: fxCluster, Kind: "Cluster", Name: "dev/ci1"})
	f.add(Node{ID: fxNS, Kind: "Namespace", Name: "app", ParentID: fxCluster})
	f.add(Node{ID: fxSecret, Kind: "Secret", Name: "db-creds", Namespace: "app", ParentID: fxNS})
	for i := 0; i < 30; i++ {
		name := "d-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		h := HealthHealthy
		if name == "d-ma" {
			h = HealthError
		}
		dep := "apps/deployment/app/" + name
		rs := "apps/replicaset/app/" + name + "-rs"
		f.add(Node{ID: dep, Kind: "Deployment", Name: name, Namespace: "app", ParentID: fxNS, Health: h})
		f.add(Node{ID: rs, Kind: "ReplicaSet", Name: name + "-rs", Namespace: "app", ParentID: dep, Health: h})
		for _, p := range []string{"1", "2", "3"} {
			pod := "core/pod/app/" + name + "-rs-" + p
			f.add(Node{ID: pod, Kind: "Pod", Name: name + "-rs-" + p, Namespace: "app", ParentID: rs, Health: h})
			f.edge(EdgeMounts, pod, fxSecret)
		}
	}
	return f
}

func TestNeighbourhood_BudgetIsAHardCap(t *testing.T) {
	snap := wideFixture().snap()
	full, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if full.OmittedNodes != 0 || len(full.Text) < 4096 {
		t.Fatalf("fixture too small to exercise truncation: %d bytes, %d omitted", len(full.Text), full.OmittedNodes)
	}

	for _, budget := range []int{MinBudget, 2048, 4096} {
		r, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: budget})
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if len(r.Text) > budget {
			t.Fatalf("budget %d: output is %d bytes", budget, len(r.Text))
		}
		if r.OmittedNodes == 0 {
			t.Fatalf("budget %d: expected truncation", budget)
		}
		last := strings.TrimRight(r.Text, "\n")
		last = last[strings.LastIndex(last, "\n")+1:]
		want := fmt.Sprintf("… truncated: %d nodes, %d edges omitted", r.OmittedNodes, r.OmittedEdges)
		if !strings.Contains(last, want) {
			t.Fatalf("budget %d: marker %q missing from last line %q", budget, want, last)
		}
		// Every line is whole: it is either a known label or the marker.
		for _, l := range strings.Split(strings.TrimRight(r.Text, "\n"), "\n") {
			l = strings.TrimSpace(strings.TrimLeft(l, "│├└─ "))
			if l == "" {
				t.Fatalf("budget %d: blank line in output:\n%s", budget, r.Text)
			}
		}
	}
}

func TestNeighbourhood_BudgetReserve(t *testing.T) {
	snap := wideFixture().snap()
	r, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: 2048, Reserve: 600})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Text)+600 > 2048 {
		t.Fatalf("reserve not honoured: %d + 600 > 2048", len(r.Text))
	}
}

func TestNeighbourhood_TruncationOrderIsTotal(t *testing.T) {
	// At the minimum budget only a fraction of the 30 Deployments fit.
	// Deepest-first drops all pods and ReplicaSets before any Deployment;
	// then healthy-before-unhealthy keeps d-ma; then label order drops d-aa
	// before d-zb.
	r, err := Neighbourhood(wideFixture().snap(), fxNS, ViewOptions{Depth: 3, Budget: MinBudget})
	if err != nil {
		t.Fatal(err)
	}
	lineWith(t, r.Text, "Deployment d-ma", "✗")
	if strings.Contains(r.Text, "ReplicaSet") || strings.Contains(r.Text, "Pod") || strings.Contains(r.Text, "mounts →") {
		t.Fatalf("deeper levels must go before any Deployment:\n%s", r.Text)
	}
	if strings.Contains(r.Text, "Deployment d-aa") {
		t.Fatalf("d-aa is the first healthy Deployment to be dropped:\n%s", r.Text)
	}
	// Identical input, identical output — the order has no hidden state.
	again, _ := Neighbourhood(wideFixture().snap(), fxNS, ViewOptions{Depth: 3, Budget: MinBudget})
	if again.Text != r.Text {
		t.Fatalf("truncation is not deterministic")
	}
}

func TestNeighbourhood_GroupsShrinkBeforeAnythingIsOmitted(t *testing.T) {
	// Sweep budgets downward over a fixture whose 30 ReplicaSets each own
	// three pods, so thirty "Pods (3)" groups exist. Invariants of the step
	// order: while any member of the d-ma group is still shown, nothing —
	// no node and no edge — has been omitted (step 1 costs no nodes); and a
	// member never outlives its header.
	snap := wideFixture().snap()
	full, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	sawShrunk := false
	for budget := len(full.Text) + 64; budget >= MinBudget; budget -= 64 {
		r, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: budget})
		if err != nil {
			t.Fatal(err)
		}
		// A connector immediately before the bare name is a real member row;
		// plain substring containment also matches the Secret's own incoming
		// "← mounts Pod d-ma-rs-1" edge line (rendered elsewhere in the tree),
		// which would falsely count as the member still being shown.
		member := strings.Contains(r.Text, "├─ d-ma-rs-1") || strings.Contains(r.Text, "└─ d-ma-rs-1")
		header := strings.Contains(r.Text, "Pods (3)")
		if member && (r.OmittedNodes > 0 || r.OmittedEdges > 0) {
			t.Fatalf("budget %d: members shown yet rows omitted — groups did not shrink first:\n%s", budget, r.Text)
		}
		if member && !header {
			t.Fatalf("budget %d: member without its header:\n%s", budget, r.Text)
		}
		if header && !member {
			sawShrunk = true
		}
	}
	if !sawShrunk {
		t.Fatal("sweep never observed a shrunk group; fixture or budgets need adjusting")
	}
}

// TestNeighbourhood_TruncationAccountingMatchesTheRender guards the running
// byte total the truncation loop keeps instead of re-rendering after every
// removal. Two ways it could drift from reality, both caught here across a
// fine budget sweep: the total under-counts, so the output overshoots the
// budget it promised; or the omission counters stop matching what was
// actually dropped, so the marker lies about it.
func TestNeighbourhood_TruncationAccountingMatchesTheRender(t *testing.T) {
	snap := wideFixture().snap()
	full, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	const reserve = 155 // a footer-sized tail, as kscope map passes
	sawTruncation, sawWhole := false, false
	for budget := len(full.Text) + 64; budget >= MinBudget+reserve; budget -= 13 {
		r, err := Neighbourhood(snap, fxNS, ViewOptions{Depth: 3, Budget: budget, Reserve: reserve})
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if len(r.Text)+reserve > budget {
			t.Fatalf("budget %d: output is %d bytes plus %d reserved", budget, len(r.Text), reserve)
		}
		marker := fmt.Sprintf("… truncated: %d nodes, %d edges omitted", r.OmittedNodes, r.OmittedEdges)
		if r.OmittedNodes == 0 && r.OmittedEdges == 0 {
			sawWhole = true
			if strings.Contains(r.Text, "truncated:") {
				t.Fatalf("budget %d: marker printed with nothing omitted:\n%s", budget, r.Text)
			}
			continue
		}
		sawTruncation = true
		last := strings.TrimRight(r.Text, "\n")
		last = last[strings.LastIndex(last, "\n")+1:]
		if !strings.HasSuffix(last, marker) {
			t.Fatalf("budget %d: last line %q does not report %q", budget, last, marker)
		}
		if n := strings.Count(r.Text, "truncated:"); n != 1 {
			t.Fatalf("budget %d: %d truncation markers", budget, n)
		}
	}
	if !sawTruncation || !sawWhole {
		t.Fatalf("sweep needs both cases: truncated=%v whole=%v", sawTruncation, sawWhole)
	}
}

// BenchmarkNeighbourhood_Truncation is the shape the truncation loop has to
// keep: a wide focus (the cluster root, which is what an agent reads out of
// `kscope info` and maps next) at the default budget, so nearly every row is
// removed. Re-rendering per removal made this quadratic — 24s at 11,761
// nodes. Run with -benchtime 1x over the sizes to see the curve.
func BenchmarkNeighbourhood_Truncation(b *testing.B) {
	for _, size := range []int{1471, 2941, 5881, 11761} {
		snap := benchFixture(size)
		b.Run(strconv.Itoa(len(snap.Nodes))+"nodes", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := Neighbourhood(snap, "cluster", ViewOptions{Depth: 3, Budget: DefaultBudget}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchFixture is an all-namespaces snapshot of about `total` nodes:
// cluster → namespaces → Deployments → ReplicaSets → Pods, every Pod mounting
// one shared Secret. Deployments own a ReplicaSet, so they are not leaves and
// never group — the row count grows with the snapshot, which is the case
// truncation has to survive.
func benchFixture(total int) Snapshot {
	f := &fixture{}
	f.add(Node{ID: "cluster", Kind: "Cluster", Name: "dev/ci1", Kubectl: "kubectl cluster-info"})
	f.add(Node{ID: fxSecret, Kind: "Secret", Name: "db-creds", Namespace: "infra", ParentID: "cluster"})
	for i, count := 0, 2; count < total; i++ {
		ns := "ns-" + strconv.Itoa(i)
		nsID := "core/namespace/" + ns
		f.add(Node{ID: nsID, Kind: "Namespace", Name: ns, ParentID: "cluster"})
		count++
		for j := 0; j < 5 && count < total; j++ {
			name := fmt.Sprintf("app-%d-%d", i, j)
			dep, rs := "apps/deployment/"+ns+"/"+name, "apps/replicaset/"+ns+"/"+name
			h := HealthHealthy
			if j == 2 {
				h = HealthError
			}
			f.add(Node{ID: dep, Kind: "Deployment", Name: name, Namespace: ns, ParentID: nsID, Health: h})
			f.add(Node{ID: rs, Kind: "ReplicaSet", Name: name + "-rs", Namespace: ns, ParentID: dep, Health: h})
			count += 2
			for p := 0; p < 3 && count < total; p++ {
				pod := "core/pod/" + ns + "/" + name + "-" + strconv.Itoa(p)
				f.add(Node{ID: pod, Kind: "Pod", Name: name + "-" + strconv.Itoa(p), Namespace: ns, ParentID: rs, Health: h})
				f.edge(EdgeMounts, pod, fxSecret)
				count++
			}
		}
	}
	return f.snap()
}

// A ParentID cycle is malformed input (hand-written JSON, a discovery bug),
// and the ancestor walk has to survive it: terminate, render, and do it
// promptly. The old guard let the walk run len(snap.Nodes) laps of the cycle
// while prepending into a growing slice, which on a large snapshot is
// quadratic in the node count before it gives up.
func TestNeighbourhood_ParentCycleTerminates(t *testing.T) {
	f := &fixture{}
	f.add(Node{ID: "a", Kind: "Deployment", Name: "a", ParentID: "b"})
	f.add(Node{ID: "b", Kind: "Deployment", Name: "b", ParentID: "a"})
	// Bulk so that a per-lap walk would be visibly expensive.
	for i := 0; i < 5000; i++ {
		f.add(Node{ID: "pad/" + strconv.Itoa(i), Kind: "ConfigMap", Name: "pad-" + strconv.Itoa(i)})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, err := Neighbourhood(f.snap(), "a", ViewOptions{})
		if err != nil {
			t.Errorf("parent cycle: unexpected err = %v", err)
			return
		}
		// The ancestor chain is walked one lap: b is a's parent, and a
		// closes the cycle. (Below the focus the same cycle reappears as
		// descendants, but that walk is already bounded by --depth.)
		focusLine := strings.Index(r.Text, "✓ healthy")
		if focusLine < 0 {
			t.Errorf("no focus line in:\n%s", r.Text)
			return
		}
		if n := strings.Count(r.Text[:focusLine], "Deployment b"); n != 1 {
			t.Errorf("the ancestor cycle must be walked once, got %d occurrences:\n%s", n, r.Text)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a two-node ParentID cycle did not terminate promptly")
	}
}

// A ParentID naming a node that is not in the snapshot (out of scope, or
// removed between passes) simply ends the walk.
func TestNeighbourhood_DanglingParentIsNotAnError(t *testing.T) {
	f := &fixture{}
	f.add(Node{ID: "a", Kind: "Deployment", Name: "a", Namespace: "app", ParentID: "core/namespace/gone"})
	r, err := Neighbourhood(f.snap(), "a", ViewOptions{})
	if err != nil {
		t.Fatalf("dangling parent: unexpected err = %v", err)
	}
	if !strings.Contains(r.Text, "Deployment a") {
		t.Fatalf("the focus must still render:\n%s", r.Text)
	}
	if strings.Contains(r.Text, "gone") {
		t.Fatalf("a parent outside the snapshot must not be named:\n%s", r.Text)
	}
}

// measure prices every row with connectorBytes, so a row's cost cannot depend
// on which connector emit gives it — but only while the two are the same
// width. Widening one and missing the other would make the byte budget
// quietly false rather than an error, which is the failure ErrSkeleton
// exists to turn into a loud one.
func TestConnectorsAreOneWidth(t *testing.T) {
	if len(connectorBytes) != len(lastConnectorBytes) {
		t.Fatalf("connectors differ in bytes: %d vs %d", len(connectorBytes), len(lastConnectorBytes))
	}
	for _, c := range []string{connectorBytes, lastConnectorBytes} {
		if n := utf8.RuneCountInString(c); n != connectorRunes {
			t.Fatalf("connector %q is %d runes, connectorRunes says %d", c, n, connectorRunes)
		}
	}
}
