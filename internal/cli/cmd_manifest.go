package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
)

const manifestSynopsis = `kscope manifest <name> [--namespace NS] [--kind K] [--data-dir DIR]

Print one resource's redacted YAML as captured at discovery time. The
staleness footer is appended as a YAML comment, so the output stays valid
YAML when redirected. Exit 2 if the resource is synthetic or has no manifest.

Unlike map, this output is NOT bounded: there is no --budget, and the stored
YAML is printed whole. A ConfigMap holding an embedded file can be megabytes.
Truncating it would be worse — a cut-off manifest is not valid YAML and reads
as a complete one — so bound it yourself if size matters: pipe through head,
or use map when the question is about structure rather than field values.`

func init() {
	register(command{name: "manifest", summary: "print one resource's redacted YAML as captured at discovery time", synopsis: manifestSynopsis, run: runManifest})
}

func runManifest(args []string, io IO) int {
	c := newFlags("manifest", manifestSynopsis, io)
	ns := c.fs.String("namespace", "", "namespace of the resource")
	kind := c.fs.String("kind", "", "Kind or plural resource name, to break ties")
	pos, code, done := c.parse(args, io)
	if done {
		return code
	}
	if len(pos) != 1 {
		fmt.Fprintf(io.Stderr, "kscope manifest: expected exactly one <name>, got %d\nrun 'kscope manifest --help' for usage\n", len(pos))
		return ExitError
	}

	if code := checkNamespace("manifest", *ns, io); code != ExitOK {
		return code
	}

	snap, store, code := loadSnapshot(c.dataDir, io)
	if code != ExitOK {
		return code
	}
	node, code := resolveOne(snap, graph.NodeRef{Namespace: *ns, Name: pos[0], Kind: *kind}, c.dataDir, io)
	if code != ExitOK {
		return code
	}
	if node.Synthetic {
		fmt.Fprintf(io.Stderr, "%s %s is a synthetic node — a logical grouping with no API object behind it, so there is no manifest.\n", graph.Sanitize(node.Kind, graph.ShortText), graph.Sanitize(node.Name, 0))
		return ExitMiss
	}
	y, err := store.Manifest(node.ID)
	if errors.Is(err, graph.ErrNoManifest) {
		fmt.Fprintf(io.Stderr, "no manifest captured for %s %s. Manifests are captured at discovery; re-run it to pick this one up:\n%s", graph.Sanitize(node.Kind, graph.ShortText), graph.Sanitize(node.Name, 0), refreshHint(snap, c.dataDir, node.Namespace))
		return ExitMiss
	}
	if err != nil {
		fmt.Fprintf(io.Stderr, "kscope manifest: %v\n", err)
		return ExitError
	}
	if !strings.HasSuffix(y, "\n") {
		y += "\n"
	}
	fmt.Fprint(io.Stdout, y, "# ", footer(snap, c.dataDir))
	return ExitOK
}
