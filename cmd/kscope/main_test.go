package main

import (
	"bytes"
	"strings"
	"testing"
)

// exec runs the binary's body over buffers instead of the process streams.
func exec(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(append([]string{"kscope"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// The top-level --help is one of only two ways an agent discovers this
// interface (spec §6), and §5.4 puts it on stdout. An agent that runs
// `kscope --help 2>/dev/null` must still learn that the subcommands exist.
func TestHelpGoesToStdoutAndNamesTheSubcommands(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "-help"} {
		t.Run(flag, func(t *testing.T) {
			code, stdout, stderr := exec(flag)
			if code != 0 {
				t.Fatalf("code = %d, want 0 (stderr: %s)", code, stderr)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			for _, want := range []string{"map", "find", "info", "manifest"} {
				if !strings.Contains(stdout, "\n  "+want+" ") {
					t.Errorf("stdout does not list the %q command:\n%s", want, stdout)
				}
			}
			for _, want := range []string{"kscope <command> --help", "-port", "-data-dir", "-discover-all-namespaces"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout is missing %q:\n%s", want, stdout)
				}
			}
		})
	}
}

// A mistyped top-level flag must not exit 2: that is the recoverable-miss
// code (§5.3), and these are exactly the flags a miss message suggests, so an
// agent reading 2 would re-run the discovery that just failed. It is also the
// case for an older kscope that predates a suggested flag.
func TestBadFlagGoesToStderrAndExitsOne(t *testing.T) {
	for _, arg := range []string{"--bogus", "--discover-namespces=a", "-port"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr := exec(arg)
			if code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, "kscope --help") {
				t.Fatalf("stderr does not point at usage:\n%s", stderr)
			}
		})
	}
}

// Dispatch is unchanged: an unknown word is cli.Run's exit 1, never a server
// an agent would wait on forever (§3.1).
func TestUnknownWordIsDispatchedToCLI(t *testing.T) {
	code, stdout, stderr := exec("mpa", "web")
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `unknown command "mpa"`) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// flag stops parsing at the first positional. Ignoring what follows meant
// `kscope --context dev/ci1 oops --discover-namespaces=a` silently dropped
// the discovery flags and started the HTTP server instead — the hang the
// subcommand dispatch of §3.1 exists to prevent.
func TestStrayPositionalIsExitOneAndNeverStartsTheServer(t *testing.T) {
	code, stdout, stderr := exec("--context", "dev/ci1", "oops", "--discover-namespaces=a")
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `unexpected argument "oops"`) || !strings.Contains(stderr, "kscope --help") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// ...and the flags-only form still reaches the server path. An unusable port
// makes ListenAndServe fail immediately, so the test proves the path is
// reached without leaving a listener behind.
func TestFlagsOnlyStillReachesTheServer(t *testing.T) {
	code, stdout, stderr := exec("--port", "999999", "--data-dir", t.TempDir())
	if code != 1 {
		t.Fatalf("code = %d, want 1 (the listener must be the thing that failed)", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "999999") {
		t.Fatalf("stderr should carry the listener error: %q", stderr)
	}
}
