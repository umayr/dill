package main

import "testing"

func TestNamedBindingIntentTokenAware(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"up", "-f", "--expect-preflight"}, false},
		{[]string{"up", "-f", "--preflight-contract", "--quiet"}, false},
		{[]string{"up", "-f", "x", "--expect-preflight", "sha256:00"}, true},
		{[]string{"up", "--preflight-contract=dill.preflight/1"}, true},
		{[]string{"up", "--", "--expect-preflight"}, false},
	} {
		if got := namedBindingIntent(tc.args); got != tc.want {
			t.Fatalf("intent %v got %v", tc.args, got)
		}
	}
	if namedSecretIntent([]string{"up", "-f", "--secret-bindings"}) {
		t.Fatal("flag-shaped filename became secret flag")
	}
}
