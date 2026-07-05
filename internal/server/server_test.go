package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/purplespacecat/kscope/internal/graph"
)

// newTestServer wires a Server against a fresh store rooted in a temp dir so
// tests don't share disk state, and swaps the cluster-touching seams for
// deterministic fakes so tests never need a live kubeconfig.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store := graph.NewStore(filepath.Join(dir, "latest.json"))
	srv := New(store)
	srv.discover = fakeDiscover
	srv.listNamespaces = func(context.Context) ([]string, error) {
		return []string{"default", "kube-system"}, nil
	}
	return srv, dir
}

// fakeDiscover fabricates a minimal but shape-correct snapshot: a cluster
// root with one namespace child per requested namespace.
func fakeDiscover(_ context.Context, scope graph.Scope) (graph.Snapshot, error) {
	snap := graph.Snapshot{
		Scope:     scope,
		Timestamp: time.Now().UTC(),
		Cluster:   graph.ClusterMeta{Context: "test"},
		Nodes: []graph.Node{
			{ID: "cluster", Kind: "Cluster", Name: "test", Health: graph.HealthHealthy},
		},
		Edges: []graph.Edge{},
	}
	for _, ns := range scope.Namespaces {
		snap.Nodes = append(snap.Nodes, graph.Node{
			ID: "core/namespace/" + ns, Kind: "Namespace", Name: ns,
			ParentID: "cluster", Health: graph.HealthHealthy,
		})
	}
	return snap, nil
}

func TestLatest_EmptyReturns204(t *testing.T) {
	srv, _ := newTestServer(t)

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/graph/latest", nil))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", rr.Code)
	}
}

func TestRefresh_PersistsAndIsReadableAfterReload(t *testing.T) {
	srv, dir := newTestServer(t)

	body := bytes.NewBufferString(`{"namespaces":["default","kube-system"]}`)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/graph/refresh", body))

	if rr.Code != http.StatusOK {
		t.Fatalf("refresh: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}

	var snap graph.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.Nodes) == 0 {
		t.Fatalf("expected non-empty snapshot: %+v", snap)
	}
	if len(snap.Scope.Namespaces) != 2 {
		t.Fatalf("expected scope to round-trip, got %+v", snap.Scope)
	}

	// File must exist on disk.
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}

	// Simulate process restart: fresh server over same dir, still sees the snapshot.
	store2 := graph.NewStore(filepath.Join(dir, "latest.json"))
	if err := store2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	srv2 := New(store2)

	rr2 := httptest.NewRecorder()
	srv2.mux.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/graph/latest", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("after reload: want 200, got %d", rr2.Code)
	}
	var snap2 graph.Snapshot
	if err := json.Unmarshal(rr2.Body.Bytes(), &snap2); err != nil {
		t.Fatalf("decode reloaded: %v", err)
	}
	if !snap.Timestamp.Equal(snap2.Timestamp) {
		t.Fatalf("timestamp mismatch: %v vs %v", snap.Timestamp, snap2.Timestamp)
	}
	if len(snap2.Nodes) != len(snap.Nodes) {
		t.Fatalf("node count mismatch after reload")
	}
}

// Empty scope is valid and means "discover every namespace" (spec §3). The
// fake only materializes requested namespaces, so the snapshot is just the
// cluster root here — the point is the request must not be rejected.
func TestRefresh_EmptyScopeIsAccepted(t *testing.T) {
	srv, _ := newTestServer(t)

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/graph/refresh", bytes.NewBufferString(`{"namespaces":[]}`)))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestManifest_ServedAndMissing(t *testing.T) {
	srv, _ := newTestServer(t)

	snap, _ := fakeDiscover(context.Background(), graph.Scope{Namespaces: []string{"default"}})
	snap.Manifests = map[string]string{
		"core/namespace/default": "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: default\n",
	}
	if err := srv.store.Set(snap); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/node/manifest/core/namespace/default", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != snap.Manifests["core/namespace/default"] {
		t.Fatalf("manifest body mismatch: %q", got)
	}

	rr2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/node/manifest/core/pod/nope/nope", nil))
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown node: want 404, got %d", rr2.Code)
	}
}

func TestNamespaces_Lists(t *testing.T) {
	srv, _ := newTestServer(t)

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/namespaces", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var body struct {
		Namespaces []string `json:"namespaces"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Namespaces) == 0 {
		t.Fatalf("expected namespaces, got none")
	}
}
