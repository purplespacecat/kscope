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
	synopsis string
	run      func(args []string, io IO) int
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
		fmt.Fprintf(io.Stderr, "no snapshot in %s — nothing has been discovered yet. Run discovery first:\n  kscope --data-dir %s --discover-namespaces=<ns>[,<ns>...]\n", dataDir, dataDir)
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
	return "snapshot " + humanAge(now().Sub(snap.Timestamp)) + " old · context=" + snap.Scope.Context +
		" · ns=" + nsList(snap.Scope) + " · data=" + dataDir + "\n"
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

// Placeholder until Task 8: enough of "info" for the skeleton's tests.
func init() {
	register(command{name: "info", synopsis: "kscope info [--data-dir DIR]", run: func(args []string, io IO) int {
		c := newFlags("info", "kscope info [--data-dir DIR]", io)
		if _, code, done := c.parse(args, io); done {
			return code
		}
		if _, _, code := loadSnapshot(c.dataDir, io); code != ExitOK {
			return code
		}
		return ExitOK
	}})
}
