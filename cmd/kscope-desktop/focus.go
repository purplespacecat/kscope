package main

import (
	"flag"
	"log"

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
	kind      string
	name      string
}

func registerFocusFlags(fs *flag.FlagSet) *focusFlags {
	f := &focusFlags{}
	fs.StringVar(&f.context, "focus-context", "", "kubeconfig context of the resource to focus")
	fs.StringVar(&f.namespace, "focus-namespace", "", "namespace of the resource to focus")
	fs.StringVar(&f.kind, "focus-kind", "", "kind or plural resource name of the resource to focus (e.g. Deployment or deployments)")
	fs.StringVar(&f.name, "focus-name", "", "name of the resource to focus; focusing is skipped when empty")
	return f
}

func (f focusFlags) ref() graph.NodeRef {
	return graph.NodeRef{Namespace: f.namespace, Name: f.name, Kind: f.kind}
}

// focusPayload is what the frontend receives. Exactly one of ID / Missing is
// meaningful: a resource outside the current snapshot cannot be selected, and
// silently doing nothing would look like the keystroke was lost.
type focusPayload struct {
	ID        string `json:"id,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	// Context is set when the request names a different cluster than the one
	// the snapshot came from — a more useful explanation than "not found".
	Context string `json:"context,omitempty"`
}

// focus resolves a reference against the loaded snapshot, tells the frontend
// what to do, and brings the window forward. Raising happens either way: the
// user pressed a key expecting kscope to appear.
func (a *App) focus(f focusFlags) {
	if f.name == "" {
		return
	}

	payload := focusPayload{Namespace: f.namespace, Name: f.name, Kind: f.kind}
	snap, err := a.store.Get()
	if err == nil {
		if id, ok := graph.ResolveNode(snap.Nodes, f.ref()); ok {
			payload.ID = id
		} else {
			payload.Missing = true
			if f.context != "" && f.context != snap.Cluster.Context {
				payload.Context = f.context
			}
		}
	} else {
		payload.Missing = true
	}

	if payload.Missing {
		log.Printf("focus %s %s/%s: not in current snapshot", f.kind, f.namespace, f.name)
	} else {
		log.Printf("focus %s %s/%s -> %s", f.kind, f.namespace, f.name, payload.ID)
	}

	wruntime.EventsEmit(a.ctx, eventFocus, payload)
	wruntime.WindowUnminimise(a.ctx)
	wruntime.WindowShow(a.ctx)
}

// onSecondInstance handles `kscope-desktop --focus-name=...` being run while an
// instance is already up: Wails delivers the second process's arguments here
// and that process exits, so this is the k9s handoff path.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	fs := flag.NewFlagSet("second-instance", flag.ContinueOnError)
	fs.SetOutput(nil) // don't print usage into the running app's log
	f := registerFocusFlags(fs)
	// The running instance's data dir wins; ignore any second-instance value.
	fs.String("data-dir", "", "ignored in a second instance")
	fs.String("redact-extra", "", "ignored in a second instance")

	if err := fs.Parse(data.Args); err != nil {
		log.Printf("warn: ignoring unparsable second-instance args %v: %v", data.Args, err)
		return
	}
	a.focus(*f)
}
