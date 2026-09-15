package main

import (
	"errors"
	"strings"

	"github.com/purplespacecat/kscope/internal/graph"
)

// isSubcommand reports whether the first argument is a word rather than a
// flag. Any word — known or not — is handed to cli.Run, which owns the
// "unknown command" message. Falling through to the server instead would
// leave an agent waiting on an HTTP listener that never returns.
func isSubcommand(args []string) bool {
	return len(args) > 1 && !strings.HasPrefix(args[1], "-")
}

// oneShotScope builds the discovery scope for a terminal invocation. The two
// discover flags are exclusive: a namespace list, or every namespace. The
// second form exists because an all-namespaces snapshot could otherwise not
// be refreshed from the CLI at all.
func oneShotScope(nsFlag string, all bool, kubeContext string, infra, crds bool) (graph.Scope, bool, error) {
	if nsFlag == "" && !all {
		return graph.Scope{}, false, nil
	}
	if nsFlag != "" && all {
		return graph.Scope{}, true, errors.New("--discover-namespaces and --discover-all-namespaces are mutually exclusive")
	}
	scope := graph.Scope{Context: kubeContext, IncludeInfra: infra, IncludeCRDs: crds}
	if all {
		return scope, true, nil
	}
	for _, part := range strings.Split(nsFlag, ",") {
		if p := strings.TrimSpace(part); p != "" {
			scope.Namespaces = append(scope.Namespaces, p)
		}
	}
	if len(scope.Namespaces) == 0 {
		return graph.Scope{}, true, errors.New("--discover-namespaces must contain at least one namespace")
	}
	return scope, true, nil
}
