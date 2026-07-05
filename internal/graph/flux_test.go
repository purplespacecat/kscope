package graph

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func deploymentWithLabels(name, ns string, labels map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, UID: types.UID("uid-" + name), Labels: labels,
		},
		Spec:   appsv1.DeploymentSpec{Replicas: ptr(int32(1))},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
}

var fluxListKinds = map[schema.GroupVersionResource]string{
	{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"}:   "GitRepositoryList",
	{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}:   "OCIRepositoryList",
	{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"}:  "HelmRepositoryList",
	{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}: "KustomizationList",
	{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}:        "HelmReleaseList",
}

// fluxAPIResources teaches the fake discovery which Flux GVRs "exist" so
// fluxGVRs resolves them exactly like against a real API server.
var fluxAPIResources = []*metav1.APIResourceList{
	{
		GroupVersion: "source.toolkit.fluxcd.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "gitrepositories", Kind: "GitRepository", Namespaced: true},
			{Name: "ocirepositories", Kind: "OCIRepository", Namespaced: true},
			{Name: "helmrepositories", Kind: "HelmRepository", Namespaced: true},
		},
	},
	{
		GroupVersion: "kustomize.toolkit.fluxcd.io/v1",
		APIResources: []metav1.APIResource{
			{Name: "kustomizations", Kind: "Kustomization", Namespaced: true},
		},
	},
	{
		GroupVersion: "helm.toolkit.fluxcd.io/v2",
		APIResources: []metav1.APIResource{
			{Name: "helmreleases", Kind: "HelmRelease", Namespaced: true},
		},
	},
}

func ustr(m map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: m}
}

func fixtureKustomization(name string, ready bool) *unstructured.Unstructured {
	status := "True"
	if !ready {
		status = "False"
	}
	return ustr(map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
		"kind":       "Kustomization",
		"metadata": map[string]any{
			"name": name, "namespace": "flux-system", "uid": "uid-ks-" + name,
			// Flux's bootstrap Kustomization manages the others (and itself).
			"labels": map[string]any{
				"kustomize.toolkit.fluxcd.io/name":      "flux-system",
				"kustomize.toolkit.fluxcd.io/namespace": "flux-system",
			},
		},
		"spec": map[string]any{
			"path":      "./kubernetes/apps",
			"sourceRef": map[string]any{"kind": "GitRepository", "name": "homelab"},
		},
		"status": map[string]any{
			"lastAppliedRevision": "main@sha1:abc1234",
			"conditions":          []any{map[string]any{"type": "Ready", "status": status}},
		},
	})
}

func fixtureGitRepo() *unstructured.Unstructured {
	return ustr(map[string]any{
		"apiVersion": "source.toolkit.fluxcd.io/v1",
		"kind":       "GitRepository",
		"metadata":   map[string]any{"name": "homelab", "namespace": "flux-system", "uid": "uid-gr"},
		"spec":       map[string]any{"url": "ssh://git@github.com/purplespacecat/homelab.git"},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	})
}

func TestFlux_DiscoveryBackrefsAndSources(t *testing.T) {
	deploy := deploymentWithLabels("web", "app", map[string]string{
		"kustomize.toolkit.fluxcd.io/name":      "apps",
		"kustomize.toolkit.fluxcd.io/namespace": "flux-system",
	})
	cs := fake.NewClientset(activeNamespace("app"), activeNamespace("flux-system"), deploy)
	cs.Discovery().(*fakediscovery.FakeDiscovery).Resources = fluxAPIResources

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), fluxListKinds,
		// The bootstrap Kustomization manages "apps" (and itself, via labels).
		fixtureKustomization("apps", true),
		fixtureKustomization("flux-system", true),
		fixtureGitRepo(),
	)

	snap, err := discover(context.Background(), cs, dyn, ClusterMeta{},
		Scope{Namespaces: []string{"app", "flux-system"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)

	ks, ok := byID["kustomize.toolkit.fluxcd.io/kustomization/flux-system/apps"]
	if !ok || ks.Health != HealthHealthy || ks.ParentID != "core/namespace/flux-system" {
		t.Fatalf("kustomization node wrong: %+v", ks)
	}
	gr, ok := byID["source.toolkit.fluxcd.io/gitrepository/flux-system/homelab"]
	if !ok {
		t.Fatal("gitrepository node missing")
	}
	if len(gr.Links) == 0 || gr.Links[0].URL != "https://github.com/purplespacecat/homelab" {
		t.Fatalf("gitrepository link wrong: %+v", gr.Links)
	}

	// The deployment's GitOpsRef is enriched from the resolved source.
	web := byID["apps/deployment/app/web"]
	if web.GitOps == nil {
		t.Fatal("deployment GitOps not set")
	}
	g := web.GitOps
	if g.Tool != "flux" || g.Kind != "Kustomization" || g.Name != "apps" {
		t.Fatalf("gitops identity wrong: %+v", g)
	}
	if g.SourceRepo != "ssh://git@github.com/purplespacecat/homelab.git" ||
		g.SourcePath != "./kubernetes/apps" ||
		g.Revision != "main@sha1:abc1234" {
		t.Fatalf("gitops source not enriched: %+v", g)
	}
	if g.WebURL != "https://github.com/purplespacecat/homelab/tree/abc1234/kubernetes/apps" {
		t.Fatalf("webURL wrong: %q", g.WebURL)
	}

	edges := make(map[string]bool, len(snap.Edges))
	for _, e := range snap.Edges {
		edges[e.ID] = true
	}
	for _, id := range []string{
		"apps/deployment/app/web -managed-by-> kustomize.toolkit.fluxcd.io/kustomization/flux-system/apps",
		"kustomize.toolkit.fluxcd.io/kustomization/flux-system/apps -sourced-from-> source.toolkit.fluxcd.io/gitrepository/flux-system/homelab",
		// The apps Kustomization is itself managed by flux-system's.
		"kustomize.toolkit.fluxcd.io/kustomization/flux-system/apps -managed-by-> kustomize.toolkit.fluxcd.io/kustomization/flux-system/flux-system",
	} {
		if !edges[id] {
			t.Errorf("missing edge %q", id)
		}
	}
}

func TestFlux_SelfManagingKustomizationHasNoSelfEdge(t *testing.T) {
	cs := fake.NewClientset(activeNamespace("flux-system"))
	cs.Discovery().(*fakediscovery.FakeDiscovery).Resources = fluxAPIResources
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), fluxListKinds,
		fixtureKustomization("flux-system", true), // labeled as managed by itself
	)

	snap, err := discover(context.Background(), cs, dyn, ClusterMeta{},
		Scope{Namespaces: []string{"flux-system"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	selfID := "kustomize.toolkit.fluxcd.io/kustomization/flux-system/flux-system"
	for _, e := range snap.Edges {
		if e.Source == selfID && e.Target == selfID {
			t.Fatalf("self-loop edge present: %q", e.ID)
		}
	}
	// The GitOpsRef itself is still attached — identity info is useful.
	if byID := indexByID(t, snap); byID[selfID].GitOps == nil {
		t.Fatal("self-managed kustomization should still carry a GitOpsRef")
	}
}

func TestFlux_NotReadyIsError(t *testing.T) {
	cs := fake.NewClientset(activeNamespace("flux-system"))
	cs.Discovery().(*fakediscovery.FakeDiscovery).Resources = fluxAPIResources
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), fluxListKinds, fixtureKustomization("broken", false),
	)

	snap, err := discover(context.Background(), cs, dyn, ClusterMeta{},
		Scope{Namespaces: []string{"flux-system"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)
	if h := byID["kustomize.toolkit.fluxcd.io/kustomization/flux-system/broken"].Health; h != HealthError {
		t.Fatalf("not-ready kustomization health = %s, want error", h)
	}
}

func TestGitURLHelpers(t *testing.T) {
	cases := []struct {
		repo, ref, path, want string
	}{
		{"https://github.com/o/r.git", "abc123", "./apps", "https://github.com/o/r/tree/abc123/apps"},
		{"ssh://git@github.com/o/r.git", "main", "", "https://github.com/o/r/tree/main"},
		{"git@github.com:o/r.git", "main", "apps", "https://github.com/o/r/tree/main/apps"},
		{"https://gitlab.com/o/r.git", "main", "apps", "https://gitlab.com/o/r/-/tree/main/apps"},
		{"https://git.example.com/o/r.git", "main", "apps", "https://git.example.com/o/r"}, // unknown host → repo home
		{"oci://ghcr.io/o/r", "main", "", ""},
		{"https://github.com/o/r.git", "", "apps", "https://github.com/o/r"}, // no ref → repo home
	}
	for _, tc := range cases {
		if got := gitWebURL(tc.repo, tc.ref, tc.path); got != tc.want {
			t.Errorf("gitWebURL(%q,%q,%q) = %q, want %q", tc.repo, tc.ref, tc.path, got, tc.want)
		}
	}

	if got := refFromRevision("main@sha1:abc1234"); got != "abc1234" {
		t.Errorf("refFromRevision sha = %q", got)
	}
	if got := refFromRevision("v1.2.3"); got != "v1.2.3" {
		t.Errorf("refFromRevision plain = %q", got)
	}
}
