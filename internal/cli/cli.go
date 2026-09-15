// Package cli implements the agent-facing subcommands of the kscope binary:
// map, find, info and manifest. Each is a function over an io pair that
// returns an exit code, so the contract of docs/agent-cli.md §5 — what goes
// to stdout, what goes to stderr, which code means what — is tested without
// spawning a process. main.go only forwards os.Args.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/purplespacecat/kscope/internal/graph"
	"github.com/purplespacecat/kscope/internal/paths"
)

// Exit codes are part of the interface: an agent branches on them without
// parsing prose (spec §5.3).
const (
	ExitOK         = 0 // resolved and printed
	ExitError      = 1 // bad arguments, unknown command, unreadable store
	ExitMiss       = 2 // recoverable: out of scope, ambiguous, no manifest
	ExitNoSnapshot = 3 // nothing discovered yet
)

// IO is where a command writes. The projection goes to Stdout and nothing
// else does; every diagnostic goes to Stderr (spec §5.4).
type IO struct {
	Stdout io.Writer
	Stderr io.Writer
}

// now is swapped by tests so the age in the footer is deterministic.
var now = time.Now

type command struct {
	name     string
	summary  string // one line, for `kscope --help`
	synopsis string
	run      func(args []string, io IO) int
}

// Summary is one subcommand as `kscope --help` lists it. Exported so the
// top-level help cannot list a command that does not exist, or miss one.
type Summary struct {
	Name string
	Text string
}

// Summaries returns every subcommand in registration order.
func Summaries() []Summary {
	out := make([]Summary, len(commands))
	for i, c := range commands {
		out[i] = Summary{Name: c.name, Text: c.summary}
	}
	return out
}

// commands is filled in by the cmd_*.go files' init functions, so adding a
// subcommand is one file and one registration.
var commands []command

func register(c command) { commands = append(commands, c) }

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// Run dispatches args[0] to its subcommand. An unknown word is exit 1: the
// alternative — falling through to the server — would leave an agent waiting
// on an HTTP listener that never returns (spec §3.1).
func Run(args []string, io IO) int {
	if len(args) == 0 {
		fmt.Fprintln(io.Stderr, "kscope: missing command")
		return ExitError
	}
	c, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(io.Stderr, "kscope: unknown command %q\n", args[0])
		fmt.Fprintln(io.Stderr, "commands: "+commandNames())
		return ExitError
	}
	return c.run(args[1:], io)
}

func commandNames() string {
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.name
	}
	return strings.Join(names, ", ")
}

// common holds the flag set and the one flag every subcommand shares.
type common struct {
	fs       *flag.FlagSet
	dataDir  string
	synopsis string
}

// newFlags builds a ContinueOnError flag set. The flag package prints parse
// errors to fs.Output and then calls Usage; routing Output to stderr and
// making Usage a no-op keeps stdout clean on every error path. Usage is
// printed by parse, to stdout, only on --help.
func newFlags(name, synopsis string, io IO) *common {
	c := &common{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	c.fs.SetOutput(io.Stderr)
	c.fs.Usage = func() {}
	c.fs.StringVar(&c.dataDir, "data-dir", paths.DataDir(), "directory holding latest.json")
	c.synopsis = synopsis
	return c
}

// parse accepts flags before or after positionals — Go's flag package stops
// at the first positional, so "map web --kind deployment" would otherwise
// silently ignore --kind (spec §3.2). Returns done=true when the caller
// should return code immediately (help printed, or a parse error).
func (c *common) parse(args []string, io IO) (pos []string, code int, done bool) {
	for {
		if err := c.fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.printUsage(io.Stdout)
				return nil, ExitOK, true
			}
			fmt.Fprintf(io.Stderr, "run 'kscope %s --help' for usage\n", c.fs.Name())
			return nil, ExitError, true
		}
		rest := c.fs.Args()
		if len(rest) == 0 {
			return pos, ExitOK, false
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func (c *common) printUsage(w io.Writer) {
	fmt.Fprintln(w, c.synopsis)
	fmt.Fprintln(w)
	c.fs.SetOutput(w)
	c.fs.PrintDefaults()
}

// loadSnapshot reads the store a subcommand was pointed at. An empty store
// is exit 3 with the command that fills it: "nothing here yet" is a normal
// state with a known next step, not an error (spec §5.3).
func loadSnapshot(dataDir string, io IO) (graph.Snapshot, *graph.Store, int) {
	store := graph.NewStore(filepath.Join(dataDir, "latest.json"))
	if err := store.Load(); err != nil {
		fmt.Fprintf(io.Stderr, "kscope: read %s: %v\n", dataDir, err)
		return graph.Snapshot{}, nil, ExitError
	}
	snap, err := store.Get()
	if errors.Is(err, graph.ErrEmpty) {
		q := shellQuote(dataDir)
		fmt.Fprintf(io.Stderr, "no snapshot in %s — nothing has been discovered yet. Run discovery first:\n  kscope --data-dir %s --discover-namespaces=<ns>[,<ns>...]\n", q, q)
		return graph.Snapshot{}, nil, ExitNoSnapshot
	}
	if err != nil {
		fmt.Fprintf(io.Stderr, "kscope: %v\n", err)
		return graph.Snapshot{}, nil, ExitError
	}
	return snap, store, ExitOK
}

// footer is the staleness contract: every read command ends with it, so an
// agent always knows how old the data is and which cluster it describes.
func footer(snap graph.Snapshot, dataDir string) string {
	return "snapshot " + humanAge(now().Sub(snap.Timestamp)) + " old · context=" + snapshotContext(snap) +
		" · ns=" + nsList(snap.Scope) + " · data=" + dataDir + "\n"
}

// snapshotContext is the context a snapshot is reported under. Scope.Context
// records what discovery was *asked for* — it is empty whenever --context
// was not passed, meaning "whatever the kubeconfig's current-context is".
// Cluster.Context records what was *reached*: it is always populated with
// the resolved name, so it is what every human- and agent-facing message
// should show.
func snapshotContext(snap graph.Snapshot) string {
	if snap.Cluster.Context != "" {
		return snap.Cluster.Context
	}
	return snap.Scope.Context
}

func nsList(scope graph.Scope) string {
	if len(scope.Namespaces) == 0 {
		return "[all]"
	}
	return "[" + strings.Join(scope.Namespaces, ",") + "]"
}

// humanAge is coarse on purpose: "4h12m" tells an agent what it needs and
// nothing it doesn't.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return strconv.Itoa(h) + "h" + strconv.Itoa(m) + "m"
	default:
		days := int(d.Hours()) / 24
		h := int(d.Hours()) - days*24
		return strconv.Itoa(days) + "d" + strconv.Itoa(h) + "h"
	}
}

// refreshHint is the exact one-shot command that widens the snapshot to
// include extraNS — echoing the snapshot's own context, data dir and include
// flags so following it can never refresh a different cluster or silently
// change scope (spec §5.2).
func refreshHint(snap graph.Snapshot, dataDir, extraNS string) string {
	var sb strings.Builder
	// A snapshot that records no context at all (written before ClusterMeta
	// existed, or by hand — Store.Load accepts both) must not produce
	// "--context  --data-dir /path": the shell collapses the space, flag
	// binds "--data-dir" as the context value, /path becomes a positional
	// that stops parsing, --discover-namespaces is never seen, and what an
	// agent was told to run starts the HTTP server and blocks — the hang
	// §3.1 exists to prevent. Say so instead and let discovery resolve it.
	if ctx := snapshotContext(snap); ctx == "" {
		sb.WriteString("  # the snapshot records no context, so this discovers the kubeconfig's current-context\n")
		sb.WriteString("  kscope --data-dir " + shellQuote(dataDir) + " \\\n")
	} else {
		sb.WriteString("  kscope --context " + shellQuote(ctx) + " --data-dir " + shellQuote(dataDir) + " \\\n")
	}
	if len(snap.Scope.Namespaces) == 0 {
		sb.WriteString("         --discover-all-namespaces \\\n")
	} else {
		ns := append([]string(nil), snap.Scope.Namespaces...)
		if extraNS != "" && !containsString(ns, extraNS) {
			ns = append(ns, extraNS)
		}
		sb.WriteString("         --discover-namespaces=" + shellQuote(strings.Join(ns, ",")) + " \\\n")
	}
	sb.WriteString("         --include-infra=" + strconv.FormatBool(snap.Scope.IncludeInfra) +
		" --include-crds=" + strconv.FormatBool(snap.Scope.IncludeCRDs) + "\n")
	return sb.String()
}

// nsLabelRE is the DNS-1123 label rule every Kubernetes namespace obeys.
// Anything else cannot name a namespace, so rejecting it early costs nothing
// and keeps it out of the refresh command a miss message tells an agent to
// run: --namespace 'x --context prod' would otherwise be interpolated into
// that command verbatim, and Go's flag package takes the last occurrence of a
// flag, so the "refresh" would discover a different cluster and overwrite the
// snapshot with it.
var nsLabelRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// checkNamespace validates a --namespace value where the flag is read, before
// the store is touched, the way --health and --limit are validated in find.
// An empty value means "no namespace filter" and is always fine.
func checkNamespace(cmd, ns string, io IO) int {
	if ns == "" || (len(ns) <= 63 && nsLabelRE.MatchString(ns)) {
		return ExitOK
	}
	fmt.Fprintf(io.Stderr, "kscope %s: --namespace must be a DNS-1123 label — lower-case letters, digits and '-', starting and ending alphanumeric, at most 63 characters; got %q\n", cmd, ns)
	return ExitError
}

// shellSafe is the set of characters that carry no meaning to a shell, so a
// word made only of them needs no quoting. It is deliberately conservative:
// everything else — spaces, $, backticks, ;, quotes, newlines — gets quoted.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote renders s as one shell word. Suggested commands are printed for
// an agent to run, and an agent may well pipe them to a shell, so anything
// interpolated into one must survive as a single argument: a data dir with a
// space would otherwise end the flag and turn the rest into positionals,
// which silently changes the scope the command refreshes. Words that need no
// quoting are left alone so the common case stays readable.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// resolveOne turns a name (+ namespace, + kind hint) into exactly one node.
// Zero candidates is a miss; more than one is ambiguity. Both are exit 2 and
// both leave stdout untouched — an agent must never receive the wrong
// resource with a success code (spec §2, §5.2).
func resolveOne(snap graph.Snapshot, ref graph.NodeRef, dataDir string, io IO) (graph.Node, int) {
	cands := graph.ResolveCandidates(snap.Nodes, ref)
	switch len(cands) {
	case 1:
		return cands[0], ExitOK
	case 0:
		what := "name=" + ref.Name
		if ref.Namespace != "" {
			what += " namespace=" + ref.Namespace
		}
		if ref.Kind != "" {
			what += " kind=" + ref.Kind
		}
		fmt.Fprint(io.Stderr, missMessage(what, snap, dataDir, ref.Namespace))
		return graph.Node{}, ExitMiss
	}
	disambiguator := "--kind"
	if ref.Kind != "" {
		disambiguator = "--namespace"
	}
	fmt.Fprintf(io.Stderr, "Ambiguous: '%s' matches %d resources in the snapshot.\nAdd %s to choose one:\n", ref.Name, len(cands), disambiguator)
	for _, n := range cands {
		ns := ""
		if n.Namespace != "" {
			ns = "-n " + n.Namespace
		}
		fmt.Fprintf(io.Stderr, "  %-16s %-40s %s\n", n.Kind, n.Name, ns)
	}
	return graph.Node{}, ExitMiss
}

// missMessage explains a miss the way an agent can act on: what was asked,
// what the snapshot covers, and the command that would widen it.
func missMessage(what string, snap graph.Snapshot, dataDir, extraNS string) string {
	var sb strings.Builder
	sb.WriteString("No resource matching " + what + ".\n\n")
	if len(snap.Scope.Namespaces) == 0 {
		sb.WriteString("Snapshot covers all namespaces, context=" + snapshotContext(snap) + " (" + humanAge(now().Sub(snap.Timestamp)) + " old); the resource may be newer than the snapshot. To refresh:\n")
	} else {
		sb.WriteString("Snapshot scope is context=" + snapshotContext(snap) + " ns=" + nsList(snap.Scope) + " (" + humanAge(now().Sub(snap.Timestamp)) + " old);\nthe resource may exist but be out of scope. To include it:\n")
	}
	sb.WriteString(refreshHint(snap, dataDir, extraNS))
	return sb.String()
}
