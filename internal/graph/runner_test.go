package graph

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestRunner(t *testing.T, discover func(context.Context, Scope) (Snapshot, error)) (*Runner, *Store) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "latest.json"))
	return NewRunnerFunc(store, discover), store
}

func TestRunner_StoresTheSnapshotItProduced(t *testing.T) {
	r, store := newTestRunner(t, func(context.Context, Scope) (Snapshot, error) {
		return snapFor("alpha"), nil
	})

	got, err := r.Run(context.Background(), Scope{Namespaces: []string{"app"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Nodes[0].Name != "alpha" {
		t.Fatalf("returned the wrong snapshot: %+v", got.Nodes)
	}
	stored, err := store.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Nodes[0].Name != "alpha" {
		t.Fatalf("store holds %+v", stored.Nodes)
	}
}

// A failed pass must never cost the user the map they already had.
func TestRunner_FailureLeavesTheStoreAlone(t *testing.T) {
	r, store := newTestRunner(t, func(context.Context, Scope) (Snapshot, error) {
		return Snapshot{}, errors.New("list namespaces: forbidden")
	})
	if err := store.Set(snapFor("alpha")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := r.Run(context.Background(), Scope{}); err == nil {
		t.Fatal("want an error")
	}

	stored, _ := store.Get()
	if stored.Nodes[0].Name != "alpha" {
		t.Fatalf("the failed pass replaced the snapshot: %+v", stored.Nodes)
	}
}

// Rather than queue behind a pass that may take minutes, a second caller is
// told to try again. Queueing would burn the waiter's whole timeout on work it
// never asked for.
func TestRunner_SecondPassIsRefusedNotQueued(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	r, _ := newTestRunner(t, func(context.Context, Scope) (Snapshot, error) {
		close(started)
		<-release
		return snapFor("alpha"), nil
	})

	go func() { _, _ = r.Run(context.Background(), Scope{}) }()
	<-started

	// Must return immediately, not block until the first pass finishes.
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(context.Background(), Scope{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("err = %v, want ErrBusy", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Run blocked instead of returning ErrBusy")
	}

	if !r.Busy() {
		t.Fatal("Busy() should report the in-flight pass")
	}
	close(release)
}

func TestRunner_FreesTheSlotAfterAFailure(t *testing.T) {
	calls := 0
	r, _ := newTestRunner(t, func(context.Context, Scope) (Snapshot, error) {
		calls++
		return Snapshot{}, errors.New("boom")
	})

	_, _ = r.Run(context.Background(), Scope{})
	_, err := r.Run(context.Background(), Scope{})

	if errors.Is(err, ErrBusy) {
		t.Fatal("a failed pass left the runner permanently busy")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
