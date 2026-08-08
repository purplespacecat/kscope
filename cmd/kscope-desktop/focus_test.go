package main

import (
	"testing"

	"github.com/purplespacecat/kscope/internal/graph"
)

func TestNormalizeNamespace(t *testing.T) {
	cases := map[string]string{
		// k9s substitutes $NAMESPACE literally, so its all-namespaces view
		// sends "all" rather than an empty string.
		"all":          "",
		"*":            "",
		"":             "",
		"cert-manager": "cert-manager",
	}
	for in, want := range cases {
		if got := normalizeNamespace(in); got != want {
			t.Errorf("normalizeNamespace(%q) = %q, want %q", in, got, want)
		}
	}
}

// The exact argv the shipped k9s plugin produces must parse.
func TestParseFocusArgs_K9sInvocation(t *testing.T) {
	args := []string{
		"--focus-context", "default",
		"--focus-namespace", "cert-manager",
		"--focus-kind", "deployments",
		"--focus-name", "cert-manager-webhook",
	}
	f, err := parseFocusArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := graph.NodeRef{Namespace: "cert-manager", Name: "cert-manager-webhook", Kind: "deployments"}
	if got := f.ref(); got != want {
		t.Fatalf("ref = %+v, want %+v", got, want)
	}
	if f.context != "default" {
		t.Errorf("context = %q, want %q", f.context, "default")
	}
}

// A second process passes the startup flags too — it has no way to know it
// will hand off — so they must be tolerated rather than rejected.
func TestParseFocusArgs_TolerartesStartupFlags(t *testing.T) {
	args := []string{
		"--data-dir", "/somewhere/else",
		"--redact-extra", "spec.password",
		"--focus-name", "api",
	}
	f, err := parseFocusArgs(args)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.name != "api" {
		t.Fatalf("name = %q, want %q", f.name, "api")
	}
}

func TestParseFocusArgs_AllNamespacesSentinel(t *testing.T) {
	f, err := parseFocusArgs([]string{"--focus-namespace", "all", "--focus-name", "api"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ns := f.ref().Namespace; ns != "" {
		t.Fatalf("namespace = %q, want empty so it doesn't filter", ns)
	}
}

func TestParseFocusArgs_UnknownFlagIsAnError(t *testing.T) {
	if _, err := parseFocusArgs([]string{"--not-a-flag"}); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}
