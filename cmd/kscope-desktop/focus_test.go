package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/purplespacecat/kscope/internal/graph"
)

func TestNormalizeNamespace(t *testing.T) {
	cases := map[string]string{
		// k9s substitutes $NAMESPACE literally, so its all-namespaces view
		// sends "all" rather than an empty string.
		"all":          "",
		"*":            "",
		"":             "",
		"cert-manager": "cert-manager",
	}
	for in, want := range cases {
		if got := normalizeNamespace(in); got != want {
			t.Errorf("normalizeNamespace(%q) = %q, want %q", in, got, want)
		}
	}
}

// The exact argv the shipped k9s plugin produces must parse.
func TestParseFocusArgs_K9sInvocation(t *testing.T) {
	args := []string{
		"--focus-context", "default",
		"--focus-namespace", "cert-manager",
		"--focus-row-namespace", "cert-manager",
		"--focus-kind", "deployments",
		"--focus-name", "cert-manager-webhook",
	}
	f, err := parseFocusArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := graph.NodeRef{Namespace: "cert-manager", Name: "cert-manager-webhook", Kind: "deployments"}
	if got := f.ref(); got != want {
		t.Fatalf("ref = %+v, want %+v", got, want)
	}
	if f.context != "default" {
		t.Errorf("context = %q, want %q", f.context, "default")
	}
}

// A second process passes the startup flags too — it has no way to know it
// will hand off — so they must be tolerated rather than rejected.
func TestParseFocusArgs_TolerartesStartupFlags(t *testing.T) {
	args := []string{
		"--data-dir", "/somewhere/else",
		"--redact-extra", "spec.password",
		"--focus-name", "api",
	}
	f, err := parseFocusArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.name != "api" {
		t.Fatalf("name = %q, want %q", f.name, "api")
	}
}

func TestParseFocusArgs_AllNamespacesSentinel(t *testing.T) {
	f, err := parseFocusArgs([]string{"--focus-namespace", "all", "--focus-name", "api"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ns := f.ref().Namespace; ns != "" {
		t.Fatalf("namespace = %q, want empty so it doesn't filter", ns)
	}
}

func TestParseFocusArgs_UnknownFlagIsAnError(t *testing.T) {
	if _, err := parseFocusArgs([]string{"--not-a-flag"}); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

func flags(ctx, ns, kind, name string) focusFlags {
	return focusFlags{context: ctx, namespace: ns, kind: kind, name: name}
}

// targetNamespace decides what a pass should be scoped to. k9s substitutes
// $NAMESPACE literally, so its all-namespaces view sends "all" — right for
// *resolving* (search everywhere), useless for *scoping*, and catastrophic if
// taken as "discover the whole cluster" from a keystroke.
func TestTargetNamespace(t *testing.T) {
	tests := []struct {
		name   string
		f      focusFlags
		want   string
		wantOK bool
	}{
		{"plain namespace", flags("", "payments", "pods", "api"), "payments", true},
		{"all with no row hint", flags("", "all", "pods", "api"), "", false},
		{"star with no row hint", flags("", "*", "pods", "api"), "", false},
		{"empty with no row hint", flags("", "", "pods", "api"), "", false},
		{
			"all, row hint supplies it",
			focusFlags{namespace: "all", rowNamespace: "payments", kind: "pods", name: "api"},
			"payments", true,
		},
		{
			// k9s leaves the token untouched when the view has no such column.
			"row hint arrived unsubstituted",
			focusFlags{namespace: "all", rowNamespace: "$COL-NAMESPACE", kind: "pods", name: "api"},
			"", false,
		},
		{
			"row hint is not a label",
			focusFlags{namespace: "all", rowNamespace: "not a namespace", kind: "pods", name: "api"},
			"", false,
		},
		{
			// A real namespace wins; the column is only a fallback.
			"row hint ignored when the primary is real",
			focusFlags{namespace: "payments", rowNamespace: "other", kind: "pods", name: "api"},
			"payments", true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.f.targetNamespace()
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("targetNamespace() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// The whole policy for "what should one handoff discover", in one pure place.
func TestDiscoveryScope(t *testing.T) {
	cur := graph.Scope{
		Context:      "dev/ci1",
		Namespaces:   []string{"app", "infra"},
		IncludeInfra: true,
		IncludeCRDs:  false,
	}

	t.Run("same context adds the namespace, and decides flags from the kind", func(t *testing.T) {
		got, ok := discoveryScope(cur, "dev/ci1", true, flags("dev/ci1", "payments", "pods", "api"))
		if !ok {
			t.Fatal("want a scope")
		}
		// Replacing the namespaces would turn a two-namespace map into a
		// one-namespace map, which is a steep price for looking at one pod.
		if !slices.Contains(got.Namespaces, "app") || !slices.Contains(got.Namespaces, "payments") {
			t.Fatalf("namespaces = %v, want the union", got.Namespaces)
		}
		// The flags are NOT inherited: a Pod needs neither infra nor custom
		// resources, whatever the current map happens to have on.
		if got.IncludeInfra || got.IncludeCRDs {
			t.Errorf("flags came from the current scope, not the kind: %+v", got)
		}
	})

	t.Run("a namespace already in scope is not duplicated", func(t *testing.T) {
		got, _ := discoveryScope(cur, "dev/ci1", true, flags("dev/ci1", "app", "pods", "api"))
		if len(got.Namespaces) != len(cur.Namespaces) {
			t.Fatalf("namespaces = %v, want no duplicate", got.Namespaces)
		}
	})

	t.Run("an all-namespaces scope stays all-namespaces", func(t *testing.T) {
		all := graph.Scope{Context: "dev/ci1"}
		got, _ := discoveryScope(all, "dev/ci1", true, flags("dev/ci1", "payments", "pods", "api"))
		if len(got.Namespaces) != 0 {
			t.Fatalf("namespaces = %v, want empty (already covers everything)", got.Namespaces)
		}
	})

	t.Run("a different cluster replaces rather than adds", func(t *testing.T) {
		got, ok := discoveryScope(cur, "dev/ci1", true, flags("prod/prod1", "payments", "pods", "api"))
		if !ok {
			t.Fatal("want a scope")
		}
		// Namespace names do not carry across clusters, so a union is meaningless.
		if !slices.Equal(got.Namespaces, []string{"payments"}) {
			t.Fatalf("namespaces = %v, want just the target", got.Namespaces)
		}
		if got.Context != "prod/prod1" {
			t.Fatalf("context = %q", got.Context)
		}
		if got.IncludeInfra {
			t.Error("infra is not carried across a cluster switch")
		}
	})

	t.Run("cold start discovers just the target", func(t *testing.T) {
		got, ok := discoveryScope(graph.Scope{}, "", false, flags("dev/ci1", "payments", "pods", "api"))
		if !ok || !slices.Equal(got.Namespaces, []string{"payments"}) || got.Context != "dev/ci1" {
			t.Fatalf("got %+v, ok=%v", got, ok)
		}
	})

	t.Run("a custom resource asks for its own kind only", func(t *testing.T) {
		got, _ := discoveryScope(cur, "dev/ci1", true, flags("dev/ci1", "payments", "certificates", "web-tls"))
		if !got.IncludeCRDs {
			t.Fatal("an unknown kind must be looked for among custom resources")
		}
		if !slices.Equal(got.CRDKinds, []string{"certificates"}) {
			t.Fatalf("CRDKinds = %v — without this the pass sweeps every CRD", got.CRDKinds)
		}
	})

	t.Run("a Flux kind needs no custom-resource sweep", func(t *testing.T) {
		got, _ := discoveryScope(cur, "dev/ci1", true, flags("dev/ci1", "payments", "kustomizations", "apps"))
		if got.IncludeCRDs {
			t.Error("the Flux pass is unconditional — this would cost a sweep for nothing")
		}
	})

	t.Run("a node turns on infra, not custom resources", func(t *testing.T) {
		plain := graph.Scope{Context: "dev/ci1", Namespaces: []string{"app"}}
		got, _ := discoveryScope(plain, "dev/ci1", true, flags("dev/ci1", "app", "nodes", "ip-10-0-0-1"))
		if !got.IncludeInfra {
			t.Error("Nodes come from the infra pass")
		}
		if got.IncludeCRDs {
			t.Error("Nodes are not custom resources")
		}
	})

	t.Run("no namespace to scope to means no pass", func(t *testing.T) {
		if _, ok := discoveryScope(cur, "dev/ci1", true, flags("dev/ci1", "all", "pods", "api")); ok {
			t.Fatal("must refuse rather than discover every namespace from a keypress")
		}
	})

	t.Run("a kind kscope never models means no pass", func(t *testing.T) {
		if _, ok := discoveryScope(cur, "dev/ci1", true, flags("dev/ci1", "payments", "endpoints", "api")); ok {
			t.Fatal("discovering cannot make an unmapped kind resolvable")
		}
	})
}

func snapWith(ctx string, nodes ...graph.Node) graph.Snapshot {
	return graph.Snapshot{
		Cluster: graph.ClusterMeta{Context: ctx},
		Scope:   graph.Scope{Context: ctx, Namespaces: []string{"app"}},
		Nodes:   nodes,
	}
}

// focusResult is what App.focus emits, extracted so it can be tested at all:
// wruntime.EventsEmit calls log.Fatalf on a context without Wails' event
// plumbing, which would take the test binary with it.
func TestFocusResult(t *testing.T) {
	pod := graph.Node{ID: "core/pod/app/api", Kind: "Pod", Name: "api", Namespace: "app"}

	t.Run("a hit resolves and asks for no discovery", func(t *testing.T) {
		got := focusResult(snapWith("dev/ci1", pod), true, flags("dev/ci1", "app", "pods", "api"))
		if got.Phase != phaseResolved || got.ID != pod.ID {
			t.Fatalf("got %+v", got)
		}
		if got.Scope != nil {
			t.Error("a resolved focus must not trigger a pass")
		}
	})

	t.Run("a miss carries the scope that would find it", func(t *testing.T) {
		got := focusResult(snapWith("dev/ci1", pod), true, flags("prod/prod1", "payments", "pods", "web"))
		if got.Phase != phaseMissing {
			t.Fatalf("phase = %q", got.Phase)
		}
		if got.Scope == nil {
			t.Fatal("want a scope to discover")
		}
		if got.Scope.Context != "prod/prod1" || !slices.Equal(got.Scope.Namespaces, []string{"payments"}) {
			t.Fatalf("scope = %+v", *got.Scope)
		}
	})

	t.Run("reports the raw namespace nowhere", func(t *testing.T) {
		// k9s sends "all" from its all-namespaces view. The payload used to
		// carry it verbatim, so the UI said "all/api" — and a frontend acting
		// on it would discover a namespace literally called "all".
		got := focusResult(snapWith("dev/ci1", pod), true, flags("dev/ci1", "all", "pods", "ghost"))
		if got.Namespace == "all" {
			t.Fatal("the sentinel leaked into the payload")
		}
	})

	t.Run("an unmapped kind is refused without a pass", func(t *testing.T) {
		// A name that resolves to nothing: ResolveNode deliberately ignores the
		// kind hint when exactly one node carries the name, so a colliding
		// fixture would resolve to that node instead of reaching this path.
		got := focusResult(snapWith("dev/ci1", pod), true, flags("dev/ci1", "app", "endpoints", "frontend"))
		if got.Phase != phaseMissing || got.Scope != nil {
			t.Fatalf("got %+v", got)
		}
		if got.Reason != reasonUnmapped {
			t.Fatalf("reason = %q, want %q", got.Reason, reasonUnmapped)
		}
	})

	t.Run("no namespace to scope to is its own reason", func(t *testing.T) {
		got := focusResult(snapWith("dev/ci1", pod), true, flags("dev/ci1", "all", "pods", "ghost"))
		if got.Reason != reasonNoNamespace || got.Scope != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("no snapshot at all still discovers", func(t *testing.T) {
		got := focusResult(graph.Snapshot{}, false, flags("dev/ci1", "payments", "pods", "api"))
		if got.Phase != phaseMissing || got.Scope == nil {
			t.Fatalf("got %+v — a cold start is the best case for this, not an edge case", got)
		}
	})
}

// k9s renders "-" in the NAMESPACE column for a row that has no namespace, and
// substitutes that into $NAMESPACE verbatim. Taken at face value it becomes a
// namespace literally called "-", which discovers nothing and cannot contain
// the resource. Observed in the wild as scope ns=[gitlab-runner,-].
func TestTargetNamespace_RejectsNonNamespaces(t *testing.T) {
	for _, ns := range []string{"-", "n/a", "<none>", "ALL", "has space", "x" + strings.Repeat("y", 63)} {
		if got, ok := (focusFlags{namespace: ns, name: "api"}).targetNamespace(); ok {
			t.Errorf("targetNamespace(%q) = (%q, true), want refused", ns, got)
		}
	}
	// And the row hint is used when the primary is one of those.
	f := focusFlags{namespace: "-", rowNamespace: "payments", name: "api"}
	if got, ok := f.targetNamespace(); !ok || got != "payments" {
		t.Fatalf("targetNamespace() = (%q, %v), want (payments, true)", got, ok)
	}
}

// A handoff pass is decided by the kind being looked at, never inherited from
// whatever the current map happens to have on. Inheriting made a hop cost the
// full custom-resource sweep (measured 38.6s on a real cluster) and dragged in
// an infra layer the user did not ask this keypress for.
func TestDiscoveryScope_DoesNotInheritFlags(t *testing.T) {
	heavy := graph.Scope{
		Context:      "dev/ci1",
		Namespaces:   []string{"gitlab-runner"},
		IncludeInfra: true,
		IncludeCRDs:  true,
	}

	got, ok := discoveryScope(heavy, "dev/ci1", true, flags("dev/ci1", "payments", "pods", "api"))
	if !ok {
		t.Fatal("want a scope")
	}
	if got.IncludeInfra {
		t.Error("IncludeInfra must come from the kind, not from the current map")
	}
	if got.IncludeCRDs {
		t.Error("IncludeCRDs must come from the kind, not from the current map")
	}
	// Namespaces are still additive: that part protects the map and nobody
	// complained about it.
	if !slices.Contains(got.Namespaces, "gitlab-runner") || !slices.Contains(got.Namespaces, "payments") {
		t.Fatalf("namespaces = %v, want the union", got.Namespaces)
	}
}

// The narrowing must survive a scope that already had custom resources on —
// that was the case where it silently did not apply, and the whole cost saving
// with it.
func TestDiscoveryScope_NarrowsEvenWhenCRDsWereAlreadyOn(t *testing.T) {
	heavy := graph.Scope{Context: "dev/ci1", Namespaces: []string{"app"}, IncludeCRDs: true}
	got, _ := discoveryScope(heavy, "dev/ci1", true, flags("dev/ci1", "app", "certificates", "web-tls"))
	if !slices.Equal(got.CRDKinds, []string{"certificates"}) {
		t.Fatalf("CRDKinds = %v, want the one kind being hunted", got.CRDKinds)
	}
}

// A "-" namespace must not become a resolution filter either: filtering on it
// matches nothing, so a resource that IS in the snapshot reads as a miss and
// triggers a pointless discovery.
func TestRef_IgnoresANamespaceThatCannotBeOne(t *testing.T) {
	if got := (focusFlags{namespace: "-", name: "api", kind: "pods"}).ref(); got.Namespace != "" {
		t.Fatalf("ref().Namespace = %q, want empty", got.Namespace)
	}
	if got := (focusFlags{namespace: "payments", name: "api"}).ref(); got.Namespace != "payments" {
		t.Fatalf("a real namespace must still filter: %q", got.Namespace)
	}
}
