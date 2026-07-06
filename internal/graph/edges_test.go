package graph

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// edgeSet runs discovery over the given objects and indexes edges by
// "source -kind-> target" for easy membership assertions.
func edgeSet(t *testing.T, scope Scope, objs ...runtime.Object) map[string]bool {
	t.Helper()
	cs := fake.NewClientset(objs...)
	snap, err := discover(context.Background(), cs, nil, ClusterMeta{}, scope)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	set := make(map[string]bool, len(snap.Edges))
	for _, e := range snap.Edges {
		set[e.ID] = true
	}
	return set
}

func TestInferEdges_PodWiring(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"},
		Spec: corev1.PodSpec{
			ServiceAccountName: "runner",
			ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "regcred"}},
			Volumes: []corev1.Volume{
				{Name: "cfg", VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}}}},
				{Name: "creds", VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: "db-creds"}}},
				{Name: "data", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-claim"}}},
				// Auto-injected token volume: must NOT create an edge.
				{Name: "kube-api-access-x7k2p", VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{
						{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}}},
					}}}},
			},
			Containers: []corev1.Container{{
				Name: "web",
				Env: []corev1.EnvVar{{
					Name: "DB_PASS",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db-creds"}, Key: "pass"}},
				}},
				EnvFrom: []corev1.EnvFromSource{{
					ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}}}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	edges := edgeSet(t, Scope{Namespaces: []string{"app"}},
		activeNamespace("app"), pod,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "app"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kube-root-ca.crt", Namespace: "app"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "app"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "regcred", Namespace: "app"}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "runner", Namespace: "app"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data-claim", Namespace: "app"}},
	)

	want := []string{
		"core/pod/app/web-0 -mounts-> core/configmap/app/app-config",
		"core/pod/app/web-0 -mounts-> core/secret/app/db-creds",
		"core/pod/app/web-0 -mounts-> core/persistentvolumeclaim/app/data-claim",
		"core/pod/app/web-0 -references-> core/secret/app/db-creds",
		"core/pod/app/web-0 -references-> core/configmap/app/app-config",
		"core/pod/app/web-0 -references-> core/secret/app/regcred",
		"core/pod/app/web-0 -uses-> core/serviceaccount/app/runner",
	}
	for _, id := range want {
		if !edges[id] {
			t.Errorf("missing edge %q", id)
		}
	}
	if edges["core/pod/app/web-0 -mounts-> core/configmap/app/kube-root-ca.crt"] {
		t.Error("kube-api-access projected volume must not produce an edge")
	}
}

func TestInferEdges_ServiceSelectorSemantics(t *testing.T) {
	edges := edgeSet(t, Scope{Namespaces: []string{"app", "other"}},
		activeNamespace("app"), activeNamespace("other"),
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
		},
		// Selector-less Service must select nothing (manual Endpoints / ExternalName).
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "app"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app", Labels: map[string]string{"app": "web"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db-1", Namespace: "app", Labels: map[string]string{"app": "db"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		// Same labels, different namespace — selectors never cross namespaces.
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-2", Namespace: "other", Labels: map[string]string{"app": "web"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	)

	if !edges["core/service/app/web -selects-> core/pod/app/web-1"] {
		t.Error("service should select matching pod in its namespace")
	}
	for id := range edges {
		switch {
		case edges["core/service/app/web -selects-> core/pod/app/db-1"]:
			t.Error("selector must not match different labels")
		case edges["core/service/app/web -selects-> core/pod/other/web-2"]:
			t.Error("selector must not cross namespaces")
		case edges["core/service/app/external -selects-> core/pod/app/web-1"]:
			t.Error("selector-less service must select nothing")
		}
		_ = id
	}
}

func TestInferEdges_NetpolEmptySelectorMatchesAllInNamespace(t *testing.T) {
	edges := edgeSet(t, Scope{Namespaces: []string{"app"}},
		activeNamespace("app"),
		&networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "app"},
			// Empty podSelector = every pod in the namespace (k8s semantics,
			// opposite of Service).
		},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "app", Labels: map[string]string{"x": "1"}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "app"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	)

	for _, pod := range []string{"a", "b"} {
		id := "networking.k8s.io/networkpolicy/app/deny-all -selects-> core/pod/app/" + pod
		if !edges[id] {
			t.Errorf("empty podSelector should match all pods; missing %q", id)
		}
	}
}

func TestInferEdges_IngressBackends(t *testing.T) {
	pt := networkingv1.PathTypePrefix
	edges := edgeSet(t, Scope{Namespaces: []string{"app"}},
		activeNamespace("app"),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "fallback", Namespace: "app"}},
		&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "main", Namespace: "app"},
			Spec: networkingv1.IngressSpec{
				DefaultBackend: &networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{Name: "fallback"}},
				Rules: []networkingv1.IngressRule{{
					IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: &pt,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{Name: "web"}},
						}},
					}},
				}},
			},
		},
	)

	for _, id := range []string{
		"networking.k8s.io/ingress/app/main -exposes-> core/service/app/web",
		"networking.k8s.io/ingress/app/main -exposes-> core/service/app/fallback",
	} {
		if !edges[id] {
			t.Errorf("missing edge %q", id)
		}
	}
}

func TestInferEdges_StorageChainAndPruning(t *testing.T) {
	cs := fake.NewClientset(
		activeNamespace("app"),
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "app"},
			Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: "pv-1"},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "pv-1"},
			Spec:       corev1.PersistentVolumeSpec{StorageClassName: "fast"},
			Status:     corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
		},
		// Unreferenced by any in-scope PVC — must NOT become a node.
		&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv-unrelated"}},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "fast"}},
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "unused"}},
	)
	snap, err := discover(context.Background(), cs, nil, ClusterMeta{}, Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	byID := indexByID(t, snap)
	if _, ok := byID["core/persistentvolume/pv-1"]; !ok {
		t.Fatal("referenced PV missing")
	}
	if _, ok := byID["core/persistentvolume/pv-unrelated"]; ok {
		t.Fatal("unreferenced PV must be pruned")
	}
	if _, ok := byID["storage.k8s.io/storageclass/fast"]; !ok {
		t.Fatal("referenced StorageClass missing")
	}
	if _, ok := byID["storage.k8s.io/storageclass/unused"]; ok {
		t.Fatal("unreferenced StorageClass must be pruned")
	}
	if pv := byID["core/persistentvolume/pv-1"]; pv.ParentID != storageGroupID {
		t.Fatalf("PV parent = %q, want storage group", pv.ParentID)
	}
	group, ok := byID[storageGroupID]
	if !ok || !group.Synthetic || group.ParentID != clusterNodeID {
		t.Fatalf("storage group wrong: %+v", group)
	}
	if sc := byID["storage.k8s.io/storageclass/fast"]; sc.ParentID != storageGroupID {
		t.Fatalf("SC parent = %q, want storage group", sc.ParentID)
	}

	edges := make(map[string]bool, len(snap.Edges))
	for _, e := range snap.Edges {
		edges[e.ID] = true
	}
	for _, id := range []string{
		"core/persistentvolumeclaim/app/data -binds-> core/persistentvolume/pv-1",
		"core/persistentvolume/pv-1 -binds-> storage.k8s.io/storageclass/fast",
	} {
		if !edges[id] {
			t.Errorf("missing edge %q", id)
		}
	}
}

func TestInferEdges_OutOfScopeTargetDropped(t *testing.T) {
	// Pod mounts a ConfigMap that was never discovered (e.g. RBAC hid it):
	// no edge, no phantom node.
	edges := edgeSet(t, Scope{Namespaces: []string{"app"}},
		activeNamespace("app"),
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "solo", Namespace: "app"},
			Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
				Name: "cfg", VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "ghost"}}},
			}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	if edges["core/pod/app/solo -mounts-> core/configmap/app/ghost"] {
		t.Error("edge to undiscovered target must be dropped")
	}
}
