package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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

func main() { os.Exit(run(os.Args, os.Stdout, os.Stderr)) }

// run is main's body over explicit streams and an explicit exit code, so the
// stream and exit-code contract is tested without spawning
// a process — the same shape internal/cli already has. args is the whole
// argv, program name included.
func run(args []string, stdout, stderr io.Writer) int {
	// Subcommands (map, find, info, manifest) are dispatched before flag
	// parsing so their own flag sets apply. A leading word — known command or
	// not — goes to cli.Run; only a flag or nothing reaches the server path.
	if isSubcommand(args) {
		return cli.Run(args[1:], cli.IO{Stdout: stdout, Stderr: stderr})
	}

	// The top-level set is ContinueOnError for the same reason the
	// subcommands' sets are (§3.2): the default ExitOnError exits 2 on an
	// undefined flag, and 2 is the code that tells an agent "out of scope,
	// widen and retry". These are precisely the flags a miss message
	// suggests, so a typo — or an older kscope without one of them — would
	// send the agent back to re-run the discovery that just failed.
	fs := flag.NewFlagSet("kscope", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// Parse errors print themselves to fs.Output, which is stderr. Usage is
	// ours to print, to stdout, and only for --help.
	fs.Usage = func() {}

	port := fs.String("port", "8080", "HTTP listen port")
	dataDir := fs.String("data-dir", paths.DataDir(), "directory for persisted snapshot")
	discoverNS := fs.String("discover-namespaces", "", "one-shot mode: run one discovery pass for these comma-separated namespaces and exit (no HTTP server)")
	discoverAll := fs.Bool("discover-all-namespaces", false, "one-shot mode: run one discovery pass over every namespace and exit")
	kubeContext := fs.String("context", "", "one-shot mode: kubeconfig context to discover against (default: current-context)")
	timeout := fs.Duration("timeout", 60*time.Second, "one-shot mode: bound on the discovery pass")
	includeInfra := fs.Bool("include-infra", true, "one-shot mode: include cluster nodes + control-plane")
	includeCRDs := fs.Bool("include-crds", true, "one-shot mode: include custom resources. Namespaced CRs honour --discover-namespaces, but cluster-scoped ones belong to no namespace and are always included — on a Crossplane- or Kyverno-heavy cluster that is most of the snapshot")
	redactExtra := fs.String("redact-extra", "", "extra comma-separated dotted paths to redact in every manifest, e.g. spec.password")

	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout, fs)
			return 0
		}
		fmt.Fprintln(stderr, "run 'kscope --help' for usage")
		return 1
	}
	// flag stops at the first positional, so without this check
	// `kscope --context dev/ci1 oops --discover-namespaces=a` would ignore
	// everything from "oops" on and start a listener an agent would wait on
	// forever. info and find reject strays for the same reason (§3.1).
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "kscope: unexpected argument %q\n", fs.Arg(0))
		fmt.Fprintln(stderr, "run 'kscope --help' for usage")
		return 1
	}

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
		fmt.Fprintf(stderr, "kscope: %v\n", err)
		return 1
	}
	if oneShot {
		return runCLIDiscover(store, scope, *timeout, stderr)
	}

	s := server.New(store)
	log.Printf("kscope listening on :%s (data-dir=%s)", *port, *dataDir)
	if err := s.Run(*port); err != nil {
		fmt.Fprintf(stderr, "kscope: %v\n", err)
		return 1
	}
	return 0
}

// printUsage is the interface an agent discovers kscope through (spec §6), so
// it goes to stdout — the one thing besides a projection that ever does
// (§5.4) — and names the subcommands, which is where everything an agent
// wants lives. The subcommand list comes from internal/cli's own registry, so
// it cannot drift from what the binary actually dispatches.
func printUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, `kscope — a bounded, agent-readable projection of a Kubernetes cluster.

usage:
  kscope <command> [flags]   read the stored snapshot
  kscope [flags]             serve the UI, or run one discovery pass and exit

commands:
`)
	for _, c := range cli.Summaries() {
		fmt.Fprintf(w, "  %-9s %s\n", c.Name, c.Text)
	}
	fmt.Fprint(w, `
'kscope <command> --help' describes that command's flags.

server and one-shot discovery flags:
`)
	fs.SetOutput(w)
	fs.PrintDefaults()
}

// runCLIDiscover is the terminal-triggered invocation path. It runs the same
// discovery code the HTTP handler runs — bounded the same way, so an agent
// that invokes it cannot hang on a slow cluster — then exits.
func runCLIDiscover(store *graph.Store, scope graph.Scope, timeout time.Duration, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	snap, err := graph.Discover(ctx, scope)
	if err != nil {
		fmt.Fprintf(stderr, "kscope: discover: %v\n", err)
		return 1
	}
	if err := store.Set(snap); err != nil {
		fmt.Fprintf(stderr, "kscope: persist snapshot: %v\n", err)
		return 1
	}
	ns := "all"
	if len(scope.Namespaces) > 0 {
		ns = strings.Join(scope.Namespaces, ",")
	}
	log.Printf("wrote snapshot: %d nodes, %d edges, context=%s namespaces=%s", len(snap.Nodes), len(snap.Edges), snap.Cluster.Context, ns)
	return 0
}
