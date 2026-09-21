package graph

import (
	"context"
	"errors"
	"time"
)

// ErrBusy reports that a discovery pass is already running. Callers are told to
// try again rather than queued: a pass can take minutes, and a caller that
// blocks on work it did not ask for burns its own deadline waiting.
var ErrBusy = errors.New("graph: a discovery pass is already running")

// DefaultDiscoveryTimeout bounds one pass. Three minutes rather than the 30s
// this replaced: a single namespace of a Crossplane-heavy cluster measured
// ~90s with custom resources on, so the old ceiling failed the very case it was
// meant to bound. It cannot become a three-minute hang — newKubeClient caps
// each individual request at 15s, so an unreachable cluster still fails fast on
// the namespace list, which is fatal.
const DefaultDiscoveryTimeout = 3 * time.Minute

// Runner serialises discovery. Every writer to the store goes through one, so
// the guard covers the HTTP handler and the desktop handoff alike — guarding
// only one of them would leave the two racing each other, and Store.Set's two
// files can be torn apart by exactly that.
type Runner struct {
	store *Store
	// sem is a one-slot semaphore rather than a Mutex: acquisition must be
	// non-blocking so a busy runner can say so instead of queueing.
	sem      chan struct{}
	discover func(context.Context, Scope) (Snapshot, error)
}

// NewRunner returns a Runner that performs real discovery.
func NewRunner(store *Store) *Runner { return NewRunnerFunc(store, Discover) }

// NewRunnerFunc is the seam for tests and for callers that supply their own
// discovery function; nothing else differs.
func NewRunnerFunc(store *Store, discover func(context.Context, Scope) (Snapshot, error)) *Runner {
	return &Runner{store: store, sem: make(chan struct{}, 1), discover: discover}
}

// Run performs one pass and replaces the stored snapshot, returning ErrBusy
// immediately if another pass holds the slot. The store is written only on
// success, so a failed or cancelled pass never costs the caller the map they
// already had.
func (r *Runner) Run(ctx context.Context, scope Scope) (Snapshot, error) {
	select {
	case r.sem <- struct{}{}:
	default:
		return Snapshot{}, ErrBusy
	}
	defer func() { <-r.sem }()

	snap, err := r.discover(ctx, scope)
	if err != nil {
		return Snapshot{}, err
	}
	if err := r.store.Set(snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// Busy reports whether a pass is in flight.
func (r *Runner) Busy() bool { return len(r.sem) > 0 }
