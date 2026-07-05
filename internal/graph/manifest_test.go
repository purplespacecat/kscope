package graph

import (
	"strings"
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
