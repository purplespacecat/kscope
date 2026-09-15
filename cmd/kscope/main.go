package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/purplespacecat/kscope/internal/cli"
	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/purplespacecat/kscope/internal/paths"
	"github.com/purplespacecat/kscope/internal/server"
)

func main() {
	// Subcommands (map, find, info, manifest) are dispatched before flag
	// parsing so their own flag sets apply. See docs/agent-cli.md §3.1.
	if isSubcommand(os.Args) {
		os.Exit(cli.Run(os.Args[1:], cli.IO{Stdout: os.Stdout, Stderr: os.Stderr}))
	}

	port := flag.String("port", "8080", "HTTP listen port")
	dataDir := flag.String("data-dir", paths.DataDir(), "directory for persisted snapshot")
	discoverNS := flag.String("discover-namespaces", "", "one-shot mode: run one discovery pass for these comma-separated namespaces and exit (no HTTP server)")
	discoverAll := flag.Bool("discover-all-namespaces", false, "one-shot mode: run one discovery pass over every namespace and exit")
	kubeContext := flag.String("context", "", "one-shot mode: kubeconfig context to discover against (default: current-context)")
	timeout := flag.Duration("timeout", 60*time.Second, "one-shot mode: bound on the discovery pass")
	includeInfra := flag.Bool("include-infra", true, "one-shot mode: include cluster nodes + control-plane")
	includeCRDs := flag.Bool("include-crds", true, "one-shot mode: include custom resources (CRDs + instances)")
	redactExtra := flag.String("redact-extra", "", "extra comma-separated dotted paths to redact in every manifest, e.g. spec.password")
	flag.Parse()

	// Must be set before any discovery runs — redaction happens at capture
	// time, never retroactively.
	for _, p := range strings.Split(*redactExtra, ",") {
		if p = strings.TrimSpace(p); p != "" {
			graph.ExtraRedactPaths = append(graph.ExtraRedactPaths, p)
		}
	}

	store := graph.NewStore(filepath.Join(*dataDir, "latest.json"))
	if err := store.Load(); err != nil {
		log.Printf("warn: could not load existing snapshot: %v", err)
	}

	scope, oneShot, err := oneShotScope(*discoverNS, *discoverAll, *kubeContext, *includeInfra, *includeCRDs)
	if err != nil {
		log.Fatal(err)
	}
	if oneShot {
		runCLIDiscover(store, scope, *timeout)
		return
	}

	s := server.New(store)
	log.Printf("kscope listening on :%s (data-dir=%s)", *port, *dataDir)
	if err := s.Run(*port); err != nil {
		log.Fatal(err)
	}
}

// runCLIDiscover is the terminal-triggered invocation path. It runs the same
// discovery code the HTTP handler runs — bounded the same way, so an agent
// that invokes it cannot hang on a slow cluster — then exits.
func runCLIDiscover(store *graph.Store, scope graph.Scope, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	snap, err := graph.Discover(ctx, scope)
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	if err := store.Set(snap); err != nil {
		log.Fatalf("persist snapshot: %v", err)
	}
	ns := "all"
	if len(scope.Namespaces) > 0 {
		ns = strings.Join(scope.Namespaces, ",")
	}
	log.Printf("wrote snapshot: %d nodes, %d edges, context=%s namespaces=%s", len(snap.Nodes), len(snap.Edges), snap.Cluster.Context, ns)
}
