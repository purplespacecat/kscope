package graph

import "testing"

func focusNodes() []Node {
	return []Node{
		{ID: "cluster", Kind: "Cluster", Name: "test"},
		{ID: "core/namespace/web", Kind: "Namespace", Name: "web"},
		{ID: "apps/deployment/web/api", Kind: "Deployment", Namespace: "web", Name: "api"},
		{ID: "core/service/web/api", Kind: "Service", Namespace: "web", Name: "api"},
		{ID: "networking.k8s.io/ingress/web/api", Kind: "Ingress", Namespace: "web", Name: "api"},
		{ID: "apps/deployment/other/api", Kind: "Deployment", Namespace: "other", Name: "api"},
		{ID: "core/node/worker-1", Kind: "Node", Name: "worker-1"},
	}
}

func TestResolveNode(t *testing.T) {
	tests := []struct {
		name string
		ref  NodeRef
		want string
		ok   bool
	}{
		{
			name: "unique by namespace and name",
			ref:  NodeRef{Namespace: "other", Name: "api"},
			want: "apps/deployment/other/api", ok: true,
		},
		{
			// k9s passes the plural resource name, not the Kind.
			name: "plural resource name breaks the tie",
			ref:  NodeRef{Namespace: "web", Name: "api", Kind: "services"},
			want: "core/service/web/api", ok: true,
		},
		{
			name: "singular kind breaks the tie",
			ref:  NodeRef{Namespace: "web", Name: "api", Kind: "Deployment"},
			want: "apps/deployment/web/api", ok: true,
		},
		{
			// "ingress" pluralises to "ingresses", not "ingresss".
			name: "es-plural kind",
			ref:  NodeRef{Namespace: "web", Name: "api", Kind: "ingresses"},
			want: "networking.k8s.io/ingress/web/api", ok: true,
		},
		{
			name: "cluster-scoped resource has no namespace",
			ref:  NodeRef{Name: "worker-1", Kind: "nodes"},
			want: "core/node/worker-1", ok: true,
		},
		{
			name: "namespace must match when supplied",
			ref:  NodeRef{Namespace: "nope", Name: "api"},
			ok:   false,
		},
		{
			name: "unknown name",
			ref:  NodeRef{Namespace: "web", Name: "ghost"},
			ok:   false,
		},
		{
			name: "empty name never matches",
			ref:  NodeRef{Namespace: "web"},
			ok:   false,
		},
		{
			// An unhelpful hint must still land somewhere rather than fail.
			name: "ambiguous with useless hint falls back to first match",
			ref:  NodeRef{Namespace: "web", Name: "api", Kind: "widgets"},
			want: "apps/deployment/web/api", ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveNode(focusNodes(), tt.ref)
			if ok != tt.ok {
				t.Fatalf("ok = %t, want %t (got id %q)", ok, tt.ok, got)
			}
			if ok && got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveCandidates(t *testing.T) {
	tests := []struct {
		name string
		ref  NodeRef
		want []string // IDs, in snapshot order
	}{
		{
			name: "unique match is a single candidate",
			ref:  NodeRef{Namespace: "other", Name: "api"},
			want: []string{"apps/deployment/other/api"},
		},
		{
			// Same name, same namespace, three kinds: the CLI must see all
			// three so it can refuse to guess.
			name: "ambiguous without a kind lists every match",
			ref:  NodeRef{Namespace: "web", Name: "api"},
			want: []string{"apps/deployment/web/api", "core/service/web/api", "networking.k8s.io/ingress/web/api"},
		},
		{
			name: "kind hint narrows to one",
			ref:  NodeRef{Namespace: "web", Name: "api", Kind: "services"},
			want: []string{"core/service/web/api"},
		},
		{
			// A hint that matches nothing must not silently widen back to
			// "all of them" and must not silently pick one either — the
			// caller needs to see the full set to explain the miss.
			name: "useless kind hint keeps the full set",
			ref:  NodeRef{Namespace: "web", Name: "api", Kind: "widgets"},
			want: []string{"apps/deployment/web/api", "core/service/web/api", "networking.k8s.io/ingress/web/api"},
		},
		{
			name: "no namespace matches across namespaces",
			ref:  NodeRef{Name: "api", Kind: "deployments"},
			want: []string{"apps/deployment/web/api", "apps/deployment/other/api"},
		},
		{name: "unknown name is empty", ref: NodeRef{Name: "ghost"}, want: nil},
		{name: "empty name is empty", ref: NodeRef{Namespace: "web"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveCandidates(focusNodes(), tt.ref)
			var ids []string
			for _, n := range got {
				ids = append(ids, n.ID)
			}
			if len(ids) != len(tt.want) {
				t.Fatalf("got %v, want %v", ids, tt.want)
			}
			for i := range ids {
				if ids[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", ids, tt.want)
				}
			}
		})
	}
}

func TestPluralize(t *testing.T) {
	cases := map[string]string{
		"deployment":    "deployments",
		"ingress":       "ingresses",
		"networkpolicy": "networkpolicies",
		"storageclass":  "storageclasses",
		"pod":           "pods",
	}
	for kind, want := range cases {
		if got := pluralize(kind); got != want {
			t.Errorf("pluralize(%q) = %q, want %q", kind, got, want)
		}
	}
}
