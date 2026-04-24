package main

import (
	"context"
	"flag"
	"log"
	"path/filepath"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/purplespacecat/kscope/internal/server"
)

func main() {
	port := flag.String("port", "8080", "HTTP listen port")
	dataDir := flag.String("data-dir", "./data", "directory for persisted snapshot")
	discoverNS := flag.String("discover-namespaces", "", "if set, run one discovery pass for these comma-separated namespaces and exit (no HTTP server)")
	flag.Parse()

	store := graph.NewStore(filepath.Join(*dataDir, "latest.json"))
	if err := store.Load(); err != nil {
		log.Printf("warn: could not load existing snapshot: %v", err)
	}

	if *discoverNS != "" {
		runCLIDiscover(store, *discoverNS)
		return
	}

	s := server.New(store)
	log.Printf("kscope listening on :%s (data-dir=%s)", *port, *dataDir)
	if err := s.Run(*port); err != nil {
		log.Fatal(err)
	}
}

// runCLIDiscover is the terminal-triggered invocation path. It runs the same
// discovery code the HTTP handler runs, then exits.
func runCLIDiscover(store *graph.Store, raw string) {
	var ns []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			ns = append(ns, p)
		}
	}
	if len(ns) == 0 {
		log.Fatal("--discover-namespaces must contain at least one namespace")
	}
	snap, err := graph.Discover(context.Background(), graph.Scope{Namespaces: ns})
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	if err := store.Set(snap); err != nil {
		log.Fatalf("persist snapshot: %v", err)
	}
	log.Printf("wrote snapshot: %d nodes, %d edges, namespaces=%v", len(snap.Nodes), len(snap.Edges), ns)
}
