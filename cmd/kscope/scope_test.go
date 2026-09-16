package main

import "testing"

func TestIsSubcommand(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"bare":             {[]string{"kscope"}, false},
		"server flags":     {[]string{"kscope", "--port", "8080"}, false},
		"help flag":        {[]string{"kscope", "-h"}, false},
		"one-shot flag":    {[]string{"kscope", "--discover-namespaces=a"}, false},
		"known subcommand": {[]string{"kscope", "map", "web"}, true},
		"unknown word":     {[]string{"kscope", "mpa", "web"}, true}, // cli.Run reports it; never the server
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isSubcommand(tc.args); got != tc.want {
				t.Fatalf("isSubcommand(%v) = %t, want %t", tc.args, got, tc.want)
			}
		})
	}
}

func TestOneShotScope(t *testing.T) {
	scope, on, err := oneShotScope(" a, b ,,c", false, "dev/ci1", true, false)
	if err != nil || !on {
		t.Fatalf("err=%v on=%t", err, on)
	}
	if len(scope.Namespaces) != 3 || scope.Namespaces[1] != "b" || scope.Context != "dev/ci1" || !scope.IncludeInfra || scope.IncludeCRDs {
		t.Fatalf("scope = %+v", scope)
	}

	scope, on, err = oneShotScope("", true, "", true, true)
	if err != nil || !on || len(scope.Namespaces) != 0 {
		t.Fatalf("all namespaces: scope=%+v on=%t err=%v", scope, on, err)
	}

	if _, on, _ := oneShotScope("", false, "", true, true); on {
		t.Fatal("neither flag set must not select one-shot mode")
	}
	if _, _, err := oneShotScope("a", true, "", true, true); err == nil {
		t.Fatal("both discover flags must be an error")
	}
	if _, _, err := oneShotScope(" , ", false, "", true, true); err == nil {
		t.Fatal("an all-blank namespace list must be an error, as before")
	}
}
