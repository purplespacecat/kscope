package graph

// What a discovery pass produces, split by which Scope flag is needed to get
// it. A caller handed only a resource kind — the k9s handoff sends the plural
// as $RESOURCE_NAME — uses these to decide what to discover, so a Pod handoff
// stays cheap and a custom-resource one still finds its target.
//
// Keep in step with the listers in discover.go, fluxResources in flux.go and
// the infra pass: a kind missing here costs a needless custom-resource sweep,
// and one listed here that is not actually discovered costs a handoff that can
// never resolve.

// discoveredKinds need neither IncludeCRDs nor IncludeInfra.
var discoveredKinds = []string{
	"Namespace", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet",
	"CronJob", "Job", "Pod", "ConfigMap", "Secret", "ServiceAccount",
	"Service", "Ingress", "NetworkPolicy", "PersistentVolumeClaim",
	// The Flux pass is unconditional (discover.go calls fluxObjects outside
	// the IncludeCRDs branch), so these are free despite being CRDs.
	"GitRepository", "OCIRepository", "HelmRepository", "Kustomization", "HelmRelease",
}

// infraKinds come from the infra pass, gated on IncludeInfra.
var infraKinds = []string{"Node"}

// unmappedKinds are built-in kinds kscope deliberately does not model. They are
// listed rather than inferred because the useful answer is "kscope doesn't map
// Endpoints", given immediately, instead of minutes of cluster reads arriving
// at the same "not found".
var unmappedKinds = []string{
	"Endpoints", "EndpointSlice", "Event", "Role", "RoleBinding",
	"ClusterRole", "ClusterRoleBinding", "HorizontalPodAutoscaler",
	"PodDisruptionBudget", "ResourceQuota", "LimitRange", "PodTemplate",
	"ReplicationController", "ComponentStatus", "Binding",
}

func anyKindMatches(kinds []string, hint string) bool {
	if hint == "" {
		return false
	}
	for _, k := range kinds {
		if KindMatches(k, hint) {
			return true
		}
	}
	return false
}

// IsDiscoveredKind reports whether a pass produces this kind without
// IncludeCRDs or IncludeInfra. hint may be a Kind ("Deployment") or a plural
// resource name ("deployments").
func IsDiscoveredKind(hint string) bool { return anyKindMatches(discoveredKinds, hint) }

// IsInfraKind reports whether reaching this kind needs IncludeInfra.
func IsInfraKind(hint string) bool { return anyKindMatches(infraKinds, hint) }

// IsUnmappedKind reports whether kscope deliberately does not model this kind,
// so no amount of discovery would make it resolvable.
func IsUnmappedKind(hint string) bool { return anyKindMatches(unmappedKinds, hint) }
