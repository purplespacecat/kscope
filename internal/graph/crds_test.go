package graph

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

var crdListKinds = map[schema.GroupVersionResource]string{
	crdGVR: "CustomResourceDefinitionList",
	{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}:   "CertificateList",
	{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}: "ClusterIssuerList",
}

func fixtureCRD(plural, group, kind, scope string) *unstructured.Unstructured {
	return ustr(map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": plural + "." + group, "uid": "uid-crd-" + plural},
		"spec": map[string]any{
			"group": group,
			"names": map[string]any{"kind": kind, "plural": plural},
			"scope": scope,
			"versions": []any{
				map[string]any{"name": "v1beta1", "served": false, "storage": false},
				map[string]any{"name": "v1", "served": true, "storage": true,
					"schema": map[string]any{"openAPIV3Schema": map[string]any{"type": "object"}}},
			},
		},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Established", "status": "True"}},
		},
	})
}

func fixtureCR(group, version, kind, ns, name string, ready bool) *unstructured.Unstructured {
	status := "True"
	if !ready {
		status = "False"
	}
	meta := map[string]any{"name": name, "uid": "uid-" + name}
	if ns != "" {
		meta["namespace"] = ns
	}
	return ustr(map[string]any{
		"apiVersion": group + "/" + version,
		"kind":       kind,
		"metadata":   meta,
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": status}},
		},
	})
}

func TestCRDs_InstancesDefinitionsAndScoping(t *testing.T) {
	cs := fake.NewClientset(activeNamespace("app"), activeNamespace("other"))
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), crdListKinds,
		fixtureCRD("certificates", "cert-manager.io", "Certificate", "Namespaced"),
		fixtureCRD("clusterissuers", "cert-manager.io", "ClusterIssuer", "Cluster"),
		fixtureCR("cert-manager.io", "v1", "Certificate", "app", "web-tls", true),
		fixtureCR("cert-manager.io", "v1", "Certificate", "other", "excluded-tls", true),
		fixtureCR("cert-manager.io", "v1", "ClusterIssuer", "", "homelab-ca", false),
	)

	// Scope covers only "app" — the "other" certificate must be filtered out.
	snap, err := discover(context.Background(), cs, dyn, ClusterMeta{},
		Scope{Namespaces: []string{"app"}, IncludeCRDs: true})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap)

	group, ok := byID[crdGroupID]
	if !ok || !group.Synthetic || group.ParentID != clusterNodeID {
		t.Fatalf("crd group wrong: %+v", group)
	}

	certCRD, ok := byID["apiextensions.k8s.io/customresourcedefinition/certificates.cert-manager.io"]
	if !ok || certCRD.ParentID != crdGroupID || certCRD.Health != HealthHealthy {
		t.Fatalf("certificate CRD node wrong: %+v", certCRD)
	}

	cert, ok := byID["cert-manager.io/certificate/app/web-tls"]
	if !ok {
		t.Fatal("in-scope certificate missing")
	}
	if cert.ParentID != "core/namespace/app" {
		t.Fatalf("namespaced CR parent = %q, want its namespace", cert.ParentID)
	}
	if !strings.Contains(cert.Kubectl, "get certificates.cert-manager.io web-tls") {
		t.Fatalf("kubectl hint should use plural.group: %q", cert.Kubectl)
	}

	if _, ok := byID["cert-manager.io/certificate/other/excluded-tls"]; ok {
		t.Fatal("out-of-scope certificate must be filtered")
	}

	// Cluster-scoped CR: lives under its definition, honest error health.
	issuer, ok := byID["cert-manager.io/clusterissuer/homelab-ca"]
	if !ok {
		t.Fatal("cluster-scoped CR missing")
	}
	if issuer.ParentID != "apiextensions.k8s.io/customresourcedefinition/clusterissuers.cert-manager.io" {
		t.Fatalf("cluster-scoped CR parent = %q, want its CRD", issuer.ParentID)
	}
	if issuer.Health != HealthError {
		t.Fatalf("issuer health = %s, want error (Ready=False)", issuer.Health)
	}

	edges := make(map[string]bool, len(snap.Edges))
	for _, e := range snap.Edges {
		edges[e.ID] = true
	}
	for _, id := range []string{
		"cert-manager.io/certificate/app/web-tls -instance-of-> apiextensions.k8s.io/customresourcedefinition/certificates.cert-manager.io",
		"cert-manager.io/clusterissuer/homelab-ca -instance-of-> apiextensions.k8s.io/customresourcedefinition/clusterissuers.cert-manager.io",
	} {
		if !edges[id] {
			t.Errorf("missing edge %q", id)
		}
	}

	// The CRD manifest must have its schema stripped (noise + size).
	manifest := snap.Manifests[certCRD.ID]
	if manifest == "" || strings.Contains(manifest, "openAPIV3Schema") {
		t.Fatalf("CRD manifest schema not stripped:\n%s", manifest)
	}
}

func TestCRDs_OffByDefaultAndPruning(t *testing.T) {
	cs := fake.NewClientset(activeNamespace("app"))
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), crdListKinds,
		fixtureCRD("certificates", "cert-manager.io", "Certificate", "Namespaced"),
		fixtureCR("cert-manager.io", "v1", "Certificate", "app", "web-tls", true),
	)

	// IncludeCRDs unset → no CRD nodes at all.
	snap, err := discover(context.Background(), cs, dyn, ClusterMeta{},
		Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if _, ok := indexByID(t, snap)[crdGroupID]; ok {
		t.Fatal("CRD group present although IncludeCRDs=false")
	}

	// Instance-less CRDs are pruned: seed only the definition.
	dyn2 := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), crdListKinds,
		fixtureCRD("certificates", "cert-manager.io", "Certificate", "Namespaced"),
	)
	snap2, err := discover(context.Background(), cs, dyn2, ClusterMeta{},
		Scope{Namespaces: []string{"app"}, IncludeCRDs: true})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byID := indexByID(t, snap2)
	if _, ok := byID["apiextensions.k8s.io/customresourcedefinition/certificates.cert-manager.io"]; ok {
		t.Fatal("instance-less CRD must be pruned")
	}
	if _, ok := byID[crdGroupID]; ok {
		t.Fatal("empty CRD group must not be pushed")
	}
}

func TestServedVersion(t *testing.T) {
	crd := fixtureCRD("things", "example.io", "Thing", "Namespaced")
	if got := servedVersion(crd); got != "v1" {
		t.Fatalf("servedVersion = %q, want storage version v1", got)
	}

	// No storage flag served → first served wins.
	noStorage := ustr(map[string]any{
		"spec": map[string]any{"versions": []any{
			map[string]any{"name": "v1alpha1", "served": true, "storage": false},
			map[string]any{"name": "v1beta1", "served": true, "storage": false},
		}},
	})
	if got := servedVersion(noStorage); got != "v1alpha1" {
		t.Fatalf("servedVersion fallback = %q, want v1alpha1", got)
	}
}

func TestConditionHealth_NoConditionsIsHealthy(t *testing.T) {
	// Objects that report nothing (ServiceMonitors, IPAddressPools, ...)
	// must not wash the tree rollups gray.
	bare := ustr(map[string]any{"metadata": map[string]any{"name": "x"}})
	if got := conditionHealth(bare, "Ready"); got != HealthHealthy {
		t.Fatalf("no-status health = %s, want healthy", got)
	}
	// Conditions present but none of the sought types nor a positive
	// suffix → honest unknown.
	other := ustr(map[string]any{
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Degraded", "status": "False"}}},
	})
	if got := conditionHealth(other, "Ready", "Available"); got != HealthUnknown {
		t.Fatalf("other-conditions health = %s, want unknown", got)
	}
}

func TestConditionHealth_PositiveSuffixFallback(t *testing.T) {
	cases := []struct {
		name     string
		condType string
		status   string
		want     Health
	}{
		{"metallb valid", "poolReconcilerValid", "True", HealthHealthy},
		{"suffix false is error", "configReconcilerValid", "False", HealthError},
		{"loaded", "ConfigLoaded", "True", HealthHealthy},
		// Polarity traps: lowercase/inverted forms must NOT match.
		{"Invalid must not match Valid", "ConfigInvalid", "True", HealthUnknown},
		{"NotReady must not match Ready", "NotReady", "True", HealthUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := ustr(map[string]any{
				"status": map[string]any{"conditions": []any{
					map[string]any{"type": tc.condType, "status": tc.status},
				}},
			})
			if got := conditionHealth(u, "Ready", "Available", "Reconciled"); got != tc.want {
				t.Fatalf("%s=%s → %s, want %s", tc.condType, tc.status, got, tc.want)
			}
		})
	}
}

func TestConditionHealth_DialectCascade(t *testing.T) {
	withConds := func(conds ...map[string]any) *unstructured.Unstructured {
		list := make([]any, len(conds))
		for i, c := range conds {
			list[i] = c
		}
		return ustr(map[string]any{"status": map[string]any{"conditions": list}})
	}
	crVocab := []string{"Ready", "Available", "Reconciled"}

	// prometheus-operator dialect: Available found via cascade.
	prom := withConds(
		map[string]any{"type": "Available", "status": "True"},
		map[string]any{"type": "Reconciled", "status": "True"},
	)
	if got := conditionHealth(prom, crVocab...); got != HealthHealthy {
		t.Fatalf("Available=True → %s, want healthy", got)
	}

	// A broken one: Available=False is an error even when Reconciled=True.
	broken := withConds(
		map[string]any{"type": "Available", "status": "False"},
		map[string]any{"type": "Reconciled", "status": "True"},
	)
	if got := conditionHealth(broken, crVocab...); got != HealthError {
		t.Fatalf("Available=False → %s, want error", got)
	}

	// Priority: Ready wins over Available when both are present.
	both := withConds(
		map[string]any{"type": "Available", "status": "True"},
		map[string]any{"type": "Ready", "status": "False"},
	)
	if got := conditionHealth(both, crVocab...); got != HealthError {
		t.Fatalf("Ready=False must outrank Available=True, got %s", got)
	}
}
