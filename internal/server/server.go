package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Server struct {
	mux *http.ServeMux
}

func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

func (s *Server) Run(port string) error {
	addr := fmt.Sprintf(":%s", port)
	return http.ListenAndServe(addr, s.mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
