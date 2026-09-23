package main

import (
	"flag"
	"io"
	"log"
	"regexp"
	"slices"

	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/wailsapp/wails/v2/pkg/options"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// focusFlags are the resource coordinates an outside tool (k9s) passes when it
// wants kscope to jump somewhere. They're plain flags so the same invocation
// works whether it starts the app or is delivered to a running instance.
type focusFlags struct {
	context   string
	namespace string
	// rowNamespace is the selected row's own namespace, from k9s's
	// $COL-NAMESPACE. It exists because $NAMESPACE carries the *view's*
	// namespace, which in an all-namespaces view is the literal "all" — fine
	// for searching everywhere, useless for deciding what to discover.
	rowNamespace string
	kind         string
	name         string
}

func registerFocusFlags(fs *flag.FlagSet) *focusFlags {
	f := &focusFlags{}
	fs.StringVar(&f.context, "focus-context", "", "kubeconfig context of the resource to focus")
	fs.StringVar(&f.namespace, "focus-namespace", "", "namespace of the resource to focus")
	fs.StringVar(&f.rowNamespace, "focus-row-namespace", "", "namespace of the selected row, when the view shows several (k9s $COL-NAMESPACE)")
	fs.StringVar(&f.kind, "focus-kind", "", "kind or plural resource name of the resource to focus (e.g. Deployment or deployments)")
	fs.StringVar(&f.name, "focus-name", "", "name of the resource to focus; focusing is skipped when empty")
	return f
}

func (f focusFlags) ref() graph.NodeRef {
	return graph.NodeRef{Namespace: normalizeNamespace(f.namespace), Name: f.name, Kind: f.kind}
}

// normalizeNamespace maps the "every namespace" sentinels a caller might pass
// onto the empty string, which ResolveNode reads as "don't filter". k9s
// substitutes $NAMESPACE literally, so in its all-namespaces view the flag
// arrives as "all" — matching nothing if taken at face value.
func normalizeNamespace(ns string) string {
	switch ns {
	case "all", "*":
		return ""
	default:
		return ns
	}
}

// nsLabel is a conservative DNS-1123 label check. The row namespace is a
// substitution that k9s may leave untouched when the view has no such column,
// so it is validated rather than trusted.
var nsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// targetNamespace is the namespace a discovery pass should cover, and whether
// there is one at all. Returning false is not a failure: it means the request
// did not say where to look, and discovering every namespace because a key was
// pressed is never the right guess.
func (f focusFlags) targetNamespace() (string, bool) {
	if ns := normalizeNamespace(f.namespace); ns != "" {
		return ns, true
	}
	if len(f.rowNamespace) <= 63 && nsLabel.MatchString(f.rowNamespace) {
		return f.rowNamespace, true
	}
	return "", false
}

// discoveryScope decides what one handoff miss should discover. Pure — no
// cluster, no clock, no store — so the policy is a table test rather than an
// integration test. Reports false when nothing should be discovered at all.
func discoveryScope(cur graph.Scope, curContext string, haveSnapshot bool, f focusFlags) (graph.Scope, bool) {
	// No amount of discovery makes a kind kscope does not model resolvable, so
	// spending minutes of cluster reads to arrive at the same "not found" is
	// pure waste.
	if graph.IsUnmappedKind(f.kind) {
		return graph.Scope{}, false
	}
	ns, ok := f.targetNamespace()
	if !ok {
		return graph.Scope{}, false
	}

	needsInfra := graph.IsInfraKind(f.kind)
	// Anything kscope does not discover by default and is not infra has to be
	// looked for among custom resources — narrowed to that one kind, or the
	// pass sweeps every CRD on the cluster.
	needsCRDs := f.kind != "" && !graph.IsDiscoveredKind(f.kind) && !needsInfra
	var crdKinds []string
	if needsCRDs {
		crdKinds = []string{f.kind}
	}

	sameCluster := haveSnapshot && (f.context == "" || f.context == curContext)
	if !sameCluster {
		// Namespace names do not carry across clusters, so carrying the old
		// scope over would be meaningless. Start clean.
		return graph.Scope{
			Context:      f.context,
			Namespaces:   []string{ns},
			IncludeInfra: needsInfra,
			IncludeCRDs:  needsCRDs,
			CRDKinds:     crdKinds,
		}, true
	}

	// Same cluster: add to what is already on screen rather than replacing it.
	// Replacing would turn a six-namespace map into a one-namespace map and
	// drop the infra layer, which is a steep price for looking at one more pod.
	next := cur
	next.IncludeInfra = cur.IncludeInfra || needsInfra
	next.IncludeCRDs = cur.IncludeCRDs || needsCRDs
	next.CRDKinds = nil
	if next.IncludeCRDs && len(cur.CRDKinds) == 0 && !cur.IncludeCRDs {
		// Only narrow when the existing scope was not already sweeping
		// everything; otherwise narrowing would silently drop CRs already shown.
		next.CRDKinds = crdKinds
	}
	// An empty namespace list already means every namespace.
	if len(cur.Namespaces) > 0 && !slices.Contains(cur.Namespaces, ns) {
		next.Namespaces = append(slices.Clone(cur.Namespaces), ns)
	}
	return next, true
}

// What a handoff came to. The frontend branches on Phase; Reason explains a
// miss it cannot act on, and Scope is the pass that would find the resource.
const (
	phaseResolved = "resolved"
	phaseMissing  = "missing"
)

// Why a miss is not worth discovering for.
const (
	reasonUnmapped    = "unmapped"     // kscope does not model this kind at all
	reasonNoNamespace = "no-namespace" // the request never said where to look
)

// focusPayload is what the frontend receives. A resolved focus carries an ID; a
// miss carries either a Scope to discover or a Reason it cannot be discovered
// for.
type focusPayload struct {
	Phase string `json:"phase"`
	ID    string `json:"id,omitempty"`
	// Namespace is the RESOLVED namespace, never the raw flag: k9s sends "all"
	// from its all-namespaces view, and passing that on reads as a namespace.
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	// Context is set when the request names a different cluster than the one
	// the snapshot came from — a more useful explanation than "not found".
	Context string `json:"context,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Scope is the pass that would bring this resource into view. Absent when
	// discovering could not help.
	Scope *graph.Scope `json:"scope,omitempty"`
}

// focusResult decides what one handoff came to, without touching the store,
// the clock or the frontend. Extracted from App.focus so it can be tested:
// wruntime.EventsEmit calls log.Fatalf on a context lacking Wails' event
// plumbing, which would take the test binary down with it.
func focusResult(snap graph.Snapshot, haveSnapshot bool, f focusFlags) focusPayload {
	ref := f.ref()
	p := focusPayload{Namespace: ref.Namespace, Name: f.name, Kind: f.kind}

	if haveSnapshot {
		if id, ok := graph.ResolveNode(snap.Nodes, ref); ok {
			p.Phase = phaseResolved
			p.ID = id
			return p
		}
		if f.context != "" && f.context != snap.Cluster.Context {
			p.Context = f.context
		}
	}

	p.Phase = phaseMissing
	scope, ok := discoveryScope(snap.Scope, snap.Cluster.Context, haveSnapshot, f)
	if !ok {
		if graph.IsUnmappedKind(f.kind) {
			p.Reason = reasonUnmapped
		} else {
			p.Reason = reasonNoNamespace
		}
		return p
	}
	p.Scope = &scope
	return p
}

func (a *App) focus(f focusFlags) {
	if f.name == "" {
		return
	}

	snap, err := a.store.Get()
	payload := focusResult(snap, err == nil, f)

	// Log the resolved reference, not the raw flags: an all-namespaces request
	// arrives as "all" but is deliberately searched as "any namespace", and
	// showing the raw value here reads like a bug.
	ref := f.ref()
	where := ref.Name
	if ref.Namespace != "" {
		where = ref.Namespace + "/" + ref.Name
	}
	switch {
	case payload.Phase == phaseResolved:
		log.Printf("focus %s %s -> %s", ref.Kind, where, payload.ID)
	case payload.Scope != nil:
		log.Printf("focus %s %s: not in snapshot, discovering %v in %q",
			ref.Kind, where, payload.Scope.Namespaces, payload.Scope.Context)
	default:
		log.Printf("focus %s %s: not in snapshot, no pass (%s)", ref.Kind, where, payload.Reason)
	}

	wruntime.EventsEmit(a.ctx, eventFocus, payload)
	wruntime.WindowUnminimise(a.ctx)
	wruntime.WindowShow(a.ctx)
}

// onSecondInstance handles `kscope-desktop --focus-name=...` being run while an
// instance is already up: Wails delivers the second process's arguments here
// and that process exits, so this is the k9s handoff path.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	f, err := parseFocusArgs(data.Args)
	if err != nil {
		log.Printf("warn: ignoring unparsable second-instance args %v: %v", data.Args, err)
		return
	}
	a.focus(f)
}

// parseFocusArgs reads the focus flags out of a second instance's argv. The
// startup flags are accepted and discarded: the running instance's data dir
// and redaction settings win, but the second process legitimately passes them
// (it doesn't know it will be handing off), so they must not be parse errors.
func parseFocusArgs(args []string) (focusFlags, error) {
	fs := flag.NewFlagSet("second-instance", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // don't print usage into the running app's log
	f := registerFocusFlags(fs)
	fs.String("data-dir", "", "ignored in a second instance")
	fs.String("redact-extra", "", "ignored in a second instance")

	if err := fs.Parse(args); err != nil {
		return focusFlags{}, err
	}
	return *f, nil
}
