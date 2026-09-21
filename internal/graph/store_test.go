package graph

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// snapFor builds a snapshot whose graph and sidecar identify each other: the
// single node's ID is also the single manifest key. A persisted pair that
// disagrees therefore came from two different calls to Set.
func snapFor(name string) Snapshot {
	id := "core/pod/app/" + name
	return Snapshot{
		Timestamp: time.Now(),
		Nodes:     []Node{{ID: id, Kind: "Pod", Name: name, Health: HealthHealthy}},
		Manifests: map[string]string{id: "kind: Pod\nmetadata:\n  name: " + name + "\n"},
	}
}

// Set writes two files. Releasing the lock between them lets a second caller
// interleave, leaving latest.json from one snapshot beside manifests.json from
// another — a mismatch that survives a restart and makes
// /api/node/manifest/{id} 404 on a node the graph says exists.
func TestStore_ConcurrentSetKeepsGraphAndManifestsTogether(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	s := NewStore(path)

	names := []string{"alpha", "bravo", "charlie", "delta"}
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Set(snapFor(names[i%len(names)])); err != nil {
				t.Errorf("Set: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// Read the persisted pair back through a cold store, the way a restart does.
	fresh := NewStore(path)
	if err := fresh.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := fresh.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("want exactly one node, got %d", len(got.Nodes))
	}
	id := got.Nodes[0].ID
	if _, ok := got.Manifests[id]; !ok {
		t.Fatalf("graph holds %q but the sidecar holds %v — the two files came from different snapshots",
			id, keysOf(got.Manifests))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
