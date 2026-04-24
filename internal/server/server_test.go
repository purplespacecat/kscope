package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/purplespacecat/kscope/internal/graph"
)

// newTestServer wires a Server against a fresh store rooted in a temp dir so
// tests don't share disk state.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store := graph.NewStore(filepath.Join(dir, "latest.json"))
	return New(store), dir
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
	if len(snap.Nodes) == 0 || len(snap.Edges) == 0 {
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

func TestRefresh_EmptyScopeReturns400(t *testing.T) {
	srv, _ := newTestServer(t)

	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/graph/refresh", bytes.NewBufferString(`{"namespaces":[]}`)))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
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
		t.Fatalf("expected stub namespaces, got none")
	}
}
