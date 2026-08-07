package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/purplespacecat/kscope/web"
)

type Server struct {
	mux   *http.ServeMux
	store *graph.Store

	// Indirection points for the cluster-touching calls, so handler tests can
	// swap in deterministic fakes instead of needing a live kubeconfig.
	discover       func(context.Context, graph.Scope) (graph.Snapshot, error)
	listNamespaces func(context.Context, string) ([]string, error)
	listContexts   func() ([]graph.KubeContext, error)
}

func New(store *graph.Store) *Server {
	s := &Server{
		mux:            http.NewServeMux(),
		store:          store,
		discover:       graph.Discover,
		listNamespaces: graph.ListNamespaces,
		listContexts:   graph.ListContexts,
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/contexts", s.handleContexts)
	s.mux.HandleFunc("GET /api/namespaces", s.handleNamespaces)
	s.mux.HandleFunc("GET /api/graph/latest", s.handleLatest)
	s.mux.HandleFunc("POST /api/graph/refresh", s.handleRefresh)
	// Node IDs contain slashes ("apps/deployment/ns/name"), so the id is a
	// trailing path wildcard rather than a single segment.
	s.mux.HandleFunc("GET /api/node/manifest/{id...}", s.handleManifest)

	// SPA: serve the embedded build at /. stdlib's mux picks the more specific
	// /api/* and /healthz patterns above before falling through to this one,
	// so no conflict.
	if h, err := spaHandler(); err != nil {
		log.Printf("warn: SPA assets unavailable: %v", err)
	} else {
		s.mux.Handle("/", h)
	}
	return s
}

// spaHandler returns a file server rooted at the embedded web/dist directory.
// If the SPA hasn't been built yet (only .gitkeep present), callers still get
// a handler that returns 404s — we don't want to break /api/*.
func spaHandler() (http.Handler, error) {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("sub fs: %w", err)
	}
	return http.FileServer(http.FS(sub)), nil
}

func (s *Server) Run(port string) error {
	addr := fmt.Sprintf(":%s", port)
	return http.ListenAndServe(addr, s.mux)
}

// Mux exposes the routing table so a host that isn't a TCP listener can serve
// the same API. The desktop shell hands this to Wails' asset server, which
// lets the frontend keep using plain same-origin fetch with no port open.
func (s *Server) Mux() http.Handler { return s.mux }

// IsAPIPath reports whether a request belongs to the JSON API rather than the
// SPA.
//
// The desktop shell needs this because Wails' dev-mode asset handler forwards
// every unmatched GET to the Vite dev server, and answers non-GET requests
// with 405 — so /api/* has to be claimed before Wails sees it. Keeping the
// predicate here means the route prefixes are declared in one place, next to
// the handlers they describe.
func IsAPIPath(p string) bool {
	return p == "/healthz" || strings.HasPrefix(p, "/api/")
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleContexts lists the kubeconfig contexts for the picker. This reads a
// local file only — no cluster is contacted, so an unreachable context still
// shows up as a choice.
func (s *Server) handleContexts(w http.ResponseWriter, _ *http.Request) {
	ctxs, err := s.listContexts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contexts": ctxs})
}

// handleNamespaces lists namespaces for the scope picker. The optional
// ?context= selects which cluster to ask; omitting it means current-context.
func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	ns, err := s.listNamespaces(r.Context(), r.URL.Query().Get("context"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespaces": ns})
}

func (s *Server) handleLatest(w http.ResponseWriter, _ *http.Request) {
	snap, err := s.store.Get()
	if err != nil {
		if errors.Is(err, graph.ErrEmpty) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// handleManifest serves one node's redacted YAML as plain text — curl-able
// and trivially rendered by the SPA.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	y, err := s.store.Manifest(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, graph.ErrEmpty) || errors.Is(err, graph.ErrNoManifest) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	if _, err := w.Write([]byte(y)); err != nil {
		log.Printf("write manifest response: %v", err)
	}
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var scope graph.Scope
	if err := json.NewDecoder(r.Body).Decode(&scope); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	// An empty namespace list is valid: it means "every namespace" (spec §3).

	// Bound discovery so a slow pass doesn't hold the request forever.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	snap, err := s.discover(ctx, scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.store.Set(snap); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
