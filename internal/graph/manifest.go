package graph

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// ExtraRedactPaths lists additional dotted paths (e.g. "spec.password") whose
// values are replaced with «redacted» in every manifest. Site-specific — set
// once at startup from the --redact-extra flag, before any discovery runs.
var ExtraRedactPaths []string

const redactedValue = "«redacted»"

// manifestYAML renders one discovered object as redacted YAML.
//
// Redaction happens HERE, before the manifest is stored or written to disk —
// secret values must never exist anywhere downstream of this function
// (spec §3.1). obj must be a pointer to the typed API object.
//
// The typed list APIs return items with an empty TypeMeta (a long-standing
// client-go quirk), so apiVersion/kind are stamped in explicitly.
func manifestYAML(obj any, apiVersion, kind string) (string, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return "", fmt.Errorf("to unstructured: %w", err)
	}
	m["apiVersion"] = apiVersion
	m["kind"] = kind
	redactManifest(m, kind)

	out, err := yaml.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal yaml: %w", err)
	}
	return string(out), nil
}

// redactManifest strips leak vectors and noise from a manifest in place.
func redactManifest(m map[string]any, kind string) {
	if md, ok := m["metadata"].(map[string]any); ok {
		// Server-side bookkeeping, huge and useless to a reader.
		delete(md, "managedFields")
		if ann, ok := md["annotations"].(map[string]any); ok {
			// This annotation embeds a full copy of the applied object — for
			// a Secret that means the plaintext values. Always strip it.
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			if len(ann) == 0 {
				delete(md, "annotations")
			}
		}
	}

	// CRD manifests embed full OpenAPI schemas — often 100KB+ of noise per
	// definition. Strip them; names/scope/versions carry the actual signal.
	if kind == "CustomResourceDefinition" {
		if spec, ok := m["spec"].(map[string]any); ok {
			if versions, ok := spec["versions"].([]any); ok {
				for _, v := range versions {
					if vm, ok := v.(map[string]any); ok {
						delete(vm, "schema")
					}
				}
			}
		}
	}

	// Secrets keep their keys (useful for wiring) but never their values.
	if kind == "Secret" {
		for _, field := range []string{"data", "stringData"} {
			if data, ok := m[field].(map[string]any); ok {
				for k := range data {
					data[k] = redactedValue
				}
			}
		}
	}

	for _, path := range ExtraRedactPaths {
		redactPath(m, strings.Split(path, "."))
	}
}

// redactPath replaces the value at a dotted path with «redacted», if present.
// Descends through nested objects only — list indexing isn't supported.
func redactPath(m map[string]any, parts []string) {
	if len(parts) == 0 {
		return
	}
	if len(parts) == 1 {
		if _, ok := m[parts[0]]; ok {
			m[parts[0]] = redactedValue
		}
		return
	}
	if next, ok := m[parts[0]].(map[string]any); ok {
		redactPath(next, parts[1:])
	}
}
