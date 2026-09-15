package graph

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManifestYAML_SecretValuesRedacted(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "app"},
		Data:       map[string][]byte{"password": []byte("hunter2")},
		StringData: map[string]string{"token": "abc123"},
	}

	y, err := manifestYAML(secret, "v1", "Secret")
	if err != nil {
		t.Fatalf("manifestYAML: %v", err)
	}
	for _, leak := range []string{"hunter2", "aHVudGVyMg", "abc123"} { // raw + base64
		if strings.Contains(y, leak) {
			t.Fatalf("secret value leaked into manifest:\n%s", y)
		}
	}
	// Keys survive — they're the useful part for understanding wiring.
	if !strings.Contains(y, "password") || !strings.Contains(y, redactedValue) {
		t.Fatalf("expected redacted keys to remain:\n%s", y)
	}
}

func TestManifestYAML_StripsNoiseAndLeakVectors(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cfg", Namespace: "app",
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"could":"embed-a-secret"}}`,
				"keep-me": "yes",
			},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
		Data: map[string]string{"key": "value"},
	}

	y, err := manifestYAML(cm, "v1", "ConfigMap")
	if err != nil {
		t.Fatalf("manifestYAML: %v", err)
	}
	if strings.Contains(y, "managedFields") || strings.Contains(y, "last-applied-configuration") {
		t.Fatalf("noise/leak vectors not stripped:\n%s", y)
	}
	if !strings.Contains(y, "keep-me") {
		t.Fatalf("unrelated annotations must survive:\n%s", y)
	}
	// Typed list items carry no TypeMeta; we stamp it in.
	if !strings.Contains(y, "apiVersion: v1") || !strings.Contains(y, "kind: ConfigMap") {
		t.Fatalf("apiVersion/kind missing:\n%s", y)
	}
}

func TestRedactPath_NestedAndMissing(t *testing.T) {
	m := map[string]any{
		"spec": map[string]any{
			"password": "topsecret",
			"nested":   map[string]any{"apiKey": "k"},
		},
	}
	redactPath(m, []string{"spec", "password"})
	redactPath(m, []string{"spec", "nested", "apiKey"})
	redactPath(m, []string{"spec", "not", "there"}) // must not panic or create keys

	spec := m["spec"].(map[string]any)
	if spec["password"] != redactedValue {
		t.Fatalf("spec.password not redacted: %v", spec["password"])
	}
	if spec["nested"].(map[string]any)["apiKey"] != redactedValue {
		t.Fatalf("nested path not redacted")
	}
	if _, ok := spec["not"]; ok {
		t.Fatalf("redactPath must not create missing paths")
	}
}

// LoadGraph skips the manifests sidecar; Manifest must still find one,
// reading the file on demand. Load's own behaviour is unchanged.
func TestStore_LoadGraphDefersTheSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	if err := NewStore(path).Set(Snapshot{
		Nodes:     []Node{{ID: "n1", Kind: "Deployment", Name: "web"}},
		Manifests: map[string]string{"n1": "kind: Deployment\n"},
	}); err != nil {
		t.Fatal(err)
	}

	s := NewStore(path)
	if err := s.LoadGraph(); err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	snap, err := s.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(snap.Nodes) != 1 {
		t.Fatalf("the graph must be loaded: %+v", snap.Nodes)
	}
	if snap.Manifests != nil {
		t.Fatalf("LoadGraph must not read the sidecar, got %v", snap.Manifests)
	}
	y, err := s.Manifest("n1")
	if err != nil {
		t.Fatalf("Manifest through the lazy path: %v", err)
	}
	if y != "kind: Deployment\n" {
		t.Fatalf("manifest = %q", y)
	}
	if _, err := s.Manifest("nope"); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("unknown node: err = %v, want ErrNoManifest", err)
	}
}

// A snapshot with no sidecar at all (pre-M2, or a hand-written latest.json)
// loads, and asking for a manifest is ErrNoManifest rather than a read error.
func TestStore_MissingSidecarIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(path, []byte(`{"nodes":[{"id":"n1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		load func(*Store) error
	}{
		{"Load", (*Store).Load},
		{"LoadGraph", (*Store).LoadGraph},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore(path)
			if err := tc.load(s); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if _, err := s.Manifest("n1"); !errors.Is(err, ErrNoManifest) {
				t.Fatalf("err = %v, want ErrNoManifest", err)
			}
		})
	}
}

// Manifest hydrates under the write lock; concurrent first calls must not
// race or deadlock. Run with -race.
func TestStore_ConcurrentFirstManifestCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	if err := NewStore(path).Set(Snapshot{
		Nodes:     []Node{{ID: "n1"}},
		Manifests: map[string]string{"n1": "kind: Deployment\n"},
	}); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if err := s.LoadGraph(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Manifest("n1"); err != nil {
				t.Errorf("Manifest: %v", err)
			}
			if _, err := s.Get(); err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}
	wg.Wait()
}
