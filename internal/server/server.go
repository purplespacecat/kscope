package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/purplespacecat/kscope/web"
)

type Server struct {
	mux   *http.ServeMux
	store *graph.Store
}

func New(store *graph.Store) *Server {
	s := &Server{mux: http.NewServeMux(), store: store}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/namespaces", s.handleNamespaces)
	s.mux.HandleFunc("GET /api/graph/latest", s.handleLatest)
	s.mux.HandleFunc("POST /api/graph/refresh", s.handleRefresh)

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

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	ns, err := graph.ListNamespaces(r.Context())
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

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var scope graph.Scope
	if err := json.NewDecoder(r.Body).Decode(&scope); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if len(scope.Namespaces) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("namespaces must be non-empty"))
		return
	}

	// Bound discovery so a slow pass doesn't hold the request forever.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	snap, err := graph.Discover(ctx, scope)
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
