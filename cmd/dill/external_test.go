package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExternalCommandDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("DILL_SECRET_POLICY", "")
	// The presence of DILL_SECRET_POLICY enables strict handling even when
	// empty, so remove it for ordinary external command tests.
	if err := os.Unsetenv("DILL_SECRET_POLICY"); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "dill-sample")
	if err := os.WriteFile(plugin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureCommand(t, "", func() error {
		code, handled := runExternalCommand([]string{"sample", "-v", "two words", "--format=json"})
		if !handled || code != 7 {
			t.Errorf("handled=%v code=%d, want true, 7", handled, code)
		}
		return nil
	})
	if err != nil || out != "-v\ntwo words\n--format=json\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestExternalCommandCannotOverrideBuiltinsOrStrictMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, name := range []string{"dill-up", "dill-preflight", "dill-sample"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"up"}, {"preflight"}, {"sample", "--secret-target", "target"}, {"../sample"}, {"--engine", "sample"}} {
		if _, handled := runExternalCommand(args); handled {
			t.Errorf("unexpected dispatch for %q", args)
		}
	}
	t.Setenv("DILL_SECRET_POLICY", "configured")
	if _, handled := runExternalCommand([]string{"sample"}); handled {
		t.Fatal("strict secret mode dispatched external executable")
	}
}

func TestExternalCommandNames(t *testing.T) {
	for _, name := range []string{"serve", "import", "a-b1"} {
		if !externalCommandName(name) {
			t.Errorf("expected valid %q", name)
		}
	}
	for _, name := range []string{"", "-x", "../x", "x/y", "X", "x y", strings.Repeat("a", 65)} {
		if externalCommandName(name) {
			t.Errorf("expected invalid %q", name)
		}
	}
}
