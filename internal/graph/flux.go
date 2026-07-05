package graph

import (
	"context"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// The Flux toolkit API groups and the resources we model (spec §4.5).
// Notification-toolkit objects (Alerts, Providers) are out of scope.
var fluxGroups = map[string]bool{
	"source.toolkit.fluxcd.io":    true,
	"kustomize.toolkit.fluxcd.io": true,
	"helm.toolkit.fluxcd.io":      true,
}

var fluxResources = map[string]string{ // resource name → Kind
	"gitrepositories":  "GitRepository",
	"ocirepositories":  "OCIRepository",
	"helmrepositories": "HelmRepository",
	"kustomizations":   "Kustomization",
	"helmreleases":     "HelmRelease",
}

// fluxGVRs asks the discovery API which Flux resources exist and at which
// version. Flux has moved kinds between v1beta2/v1 across releases, so the
// server's preferred version is resolved instead of hardcoding one. An empty
// result means Flux isn't installed — the whole pass is skipped silently.
func fluxGVRs(disc discovery.DiscoveryInterface) (map[schema.GroupVersionResource]string, error) {
	groups, err := disc.ServerGroups()
	if err != nil {
		return nil, err
	}
	out := map[schema.GroupVersionResource]string{}
	for _, g := range groups.Groups {
		if !fluxGroups[g.Name] {
			continue
		}
		gvString := g.PreferredVersion.GroupVersion
		res, err := disc.ServerResourcesForGroupVersion(gvString)
		if err != nil {
			continue // group vanished mid-flight; not fatal
		}
		gv, err := schema.ParseGroupVersion(gvString)
		if err != nil {
			continue
		}
		for _, r := range res.APIResources {
			if kind, ok := fluxResources[r.Name]; ok {
				out[gv.WithResource(r.Name)] = kind
			}
		}
	}
	return out, nil
}

// fluxObjects lists the toolkit CRs in the scoped namespaces and pushes them
// as regular nodes. Raw objects are retained for the back-reference pass.
func (b *builder) fluxObjects(ctx context.Context, disc discovery.DiscoveryInterface, dyn dynamic.Interface, namespaces []string) {
	if dyn == nil {
		return // tests that don't exercise GitOps pass nil
	}
	gvrs, err := fluxGVRs(disc)
	if err != nil {
		b.errf("flux discovery: %v", err)
		return
	}
	for gvr, kind := range gvrs {
		for _, ns := range namespaces {
			list, err := dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				if !apierrors.IsNotFound(err) {
					b.errf("%s %s: %v", gvr.Resource, ns, err)
				}
				continue
			}
			for i := range list.Items {
				b.pushFlux(&list.Items[i], gvr, kind)
			}
		}
	}
}

func (b *builder) pushFlux(u *unstructured.Unstructured, gvr schema.GroupVersionResource, kind string) {
	ns, name := u.GetNamespace(), u.GetName()
	id := nodeID(gvr.Group, kind, ns, name)
	apiVersion := gvr.GroupVersion().String()

	n := Node{
		ID:         id,
		Kind:       kind,
		Name:       name,
		Namespace:  ns,
		APIVersion: apiVersion,
		UID:        string(u.GetUID()),
		Labels:     u.GetLabels(),
		Health:     fluxHealth(u),
		Kubectl:    kubectlCmd(b.kubectx, ns, "get "+strings.ToLower(kind)+" "+name+" -o yaml"),
	}
	// Source kinds get a clickable repository link when the URL is browsable.
	if url, _, _ := unstructured.NestedString(u.Object, "spec", "url"); url != "" {
		if web := webRepoURL(url); web != "" {
			n.Links = append(n.Links, Link{Label: "Repository", URL: web})
		}
	}
	b.push(n, u.GetUID(), controllerUID(u.GetOwnerReferences()))
	// DeepCopy: redaction mutates the map, and u is still needed for the
	// back-reference pass.
	b.captureManifest(id, u.DeepCopy(), apiVersion, kind)
	b.rawFlux[id] = u
}

// fluxHealth reads the standard Flux Ready condition; a suspended object is
// surfaced as a warning (it silently stops reconciling — worth seeing).
func fluxHealth(u *unstructured.Unstructured) Health {
	if suspended, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); suspended {
		return HealthWarning
	}
	return conditionHealth(u, "Ready")
}

// fluxSource is what a managed resource inherits onto its GitOpsRef.
type fluxSource struct {
	repo, path, revision, webURL string
}

// fluxBackrefs runs after every node exists: it resolves each
// Kustomization/HelmRelease to its source, then walks all nodes attaching
// GitOpsRefs + managed-by edges via Flux's ownership labels.
func (b *builder) fluxBackrefs() {
	if len(b.rawFlux) == 0 {
		return
	}

	sources := map[string]fluxSource{} // manager node ID → resolved source
	for id, u := range b.rawFlux {
		switch u.GetKind() {
		case "Kustomization":
			sources[id] = b.resolveKustomization(u)
		case "HelmRelease":
			sources[id] = b.resolveHelmRelease(u)
		}
	}

	for i := range b.nodes {
		n := &b.nodes[i]
		if n.Labels == nil {
			continue
		}
		// kustomize-controller and helm-controller stamp everything they
		// apply with these labels — the authoritative back-reference.
		if name, ok := n.Labels["kustomize.toolkit.fluxcd.io/name"]; ok {
			ns := n.Labels["kustomize.toolkit.fluxcd.io/namespace"]
			b.attachGitOps(n, "kustomize.toolkit.fluxcd.io", "Kustomization", name, ns, sources)
		} else if name, ok := n.Labels["helm.toolkit.fluxcd.io/name"]; ok {
			ns := n.Labels["helm.toolkit.fluxcd.io/namespace"]
			b.attachGitOps(n, "helm.toolkit.fluxcd.io", "HelmRelease", name, ns, sources)
		}
	}
}

func (b *builder) attachGitOps(n *Node, group, kind, name, ns string, sources map[string]fluxSource) {
	ref := &GitOpsRef{Tool: "flux", Kind: kind, Name: name, Namespace: ns}
	managerID := nodeID(group, kind, ns, name)
	if src, ok := sources[managerID]; ok {
		ref.SourceRepo, ref.SourcePath = src.repo, src.path
		ref.Revision, ref.WebURL = src.revision, src.webURL
	}
	n.GitOps = ref
	// Flux's own bootstrap Kustomization manages itself — skip the self-loop.
	if n.ID != managerID {
		b.addEdge(EdgeManagedBy, n.ID, managerID)
	}
}

// resolveKustomization extracts source coordinates from a Kustomization and
// records the sourced-from edge.
func (b *builder) resolveKustomization(u *unstructured.Unstructured) fluxSource {
	var s fluxSource
	s.path, _, _ = unstructured.NestedString(u.Object, "spec", "path")
	s.revision, _, _ = unstructured.NestedString(u.Object, "status", "lastAppliedRevision")

	kind, _, _ := unstructured.NestedString(u.Object, "spec", "sourceRef", "kind")
	name, _, _ := unstructured.NestedString(u.Object, "spec", "sourceRef", "name")
	srcNS, _, _ := unstructured.NestedString(u.Object, "spec", "sourceRef", "namespace")
	if srcNS == "" {
		srcNS = u.GetNamespace() // sourceRef namespace defaults to its own
	}
	srcID := nodeID("source.toolkit.fluxcd.io", kind, srcNS, name)
	if src, ok := b.rawFlux[srcID]; ok {
		s.repo, _, _ = unstructured.NestedString(src.Object, "spec", "url")
	}
	selfID := nodeID("kustomize.toolkit.fluxcd.io", "Kustomization", u.GetNamespace(), u.GetName())
	b.addEdge(EdgeSourcedFrom, selfID, srcID)

	s.webURL = gitWebURL(s.repo, refFromRevision(s.revision), s.path)
	return s
}

// resolveHelmRelease extracts chart coordinates. Only the classic
// spec.chart.spec.sourceRef shape is resolved; chartRef (OCI direct) just
// yields the manager identity without source enrichment.
func (b *builder) resolveHelmRelease(u *unstructured.Unstructured) fluxSource {
	var s fluxSource
	s.path, _, _ = unstructured.NestedString(u.Object, "spec", "chart", "spec", "chart")
	s.revision, _, _ = unstructured.NestedString(u.Object, "status", "lastAppliedRevision")

	kind, _, _ := unstructured.NestedString(u.Object, "spec", "chart", "spec", "sourceRef", "kind")
	name, _, _ := unstructured.NestedString(u.Object, "spec", "chart", "spec", "sourceRef", "name")
	srcNS, _, _ := unstructured.NestedString(u.Object, "spec", "chart", "spec", "sourceRef", "namespace")
	if kind == "" || name == "" {
		return s
	}
	if srcNS == "" {
		srcNS = u.GetNamespace()
	}
	srcID := nodeID("source.toolkit.fluxcd.io", kind, srcNS, name)
	if src, ok := b.rawFlux[srcID]; ok {
		s.repo, _, _ = unstructured.NestedString(src.Object, "spec", "url")
	}
	selfID := nodeID("helm.toolkit.fluxcd.io", "HelmRelease", u.GetNamespace(), u.GetName())
	b.addEdge(EdgeSourcedFrom, selfID, srcID)

	if kind == "GitRepository" {
		s.webURL = gitWebURL(s.repo, refFromRevision(s.revision), s.path)
	}
	return s
}

// --- Git URL helpers ---------------------------------------------------------

// refFromRevision extracts a git ref usable in a tree URL from a Flux
// revision like "main@sha1:abc123". The exact sha is preferred (immutable
// link), falling back to the branch/tag name.
func refFromRevision(rev string) string {
	if i := strings.Index(rev, "sha1:"); i >= 0 {
		return rev[i+len("sha1:"):]
	}
	if i := strings.Index(rev, "@"); i > 0 {
		return rev[:i]
	}
	return rev
}

// webRepoURL normalizes a git remote URL (https, ssh://git@, scp-style
// git@host:path) to a browsable https URL without ".git". Returns "" for
// non-web schemes (oci://, s3://, ...).
func webRepoURL(repo string) string {
	r := strings.TrimSuffix(repo, ".git")
	switch {
	case strings.HasPrefix(r, "https://"), strings.HasPrefix(r, "http://"):
		return r
	case strings.HasPrefix(r, "ssh://git@"):
		return "https://" + strings.TrimPrefix(r, "ssh://git@")
	case strings.HasPrefix(r, "git@") && strings.Contains(r, ":"):
		return "https://" + strings.Replace(strings.TrimPrefix(r, "git@"), ":", "/", 1)
	default:
		return ""
	}
}

// gitWebURL builds a browsable tree deep-link for hosts whose URL layout we
// know (github, gitlab). For anything else the repo home is still returned —
// better a coarse link than a dead one.
func gitWebURL(repo, ref, path string) string {
	base := webRepoURL(repo)
	if base == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")

	var seg string
	switch {
	case host == "github.com":
		seg = "/tree/"
	case strings.Contains(host, "gitlab"):
		seg = "/-/tree/"
	default:
		return base
	}
	if ref == "" {
		return base
	}
	u := base + seg + ref
	if p := strings.Trim(path, "./"); p != "" {
		u += "/" + p
	}
	return u
}
