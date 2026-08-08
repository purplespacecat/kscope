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
