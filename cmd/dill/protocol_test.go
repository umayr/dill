//go:build darwin || linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/protocol"
)

func TestProtocolCommandInventory(t *testing.T) {
	found := false
	for _, s := range allCommands {
		if s == cmdProtocol {
			found = true
		}
	}
	if !found || closestCommand("protoco") != cmdProtocol {
		t.Fatal("missing command inventory")
	}
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"protocol"}, true}, {[]string{"protocol", "run", "--env-file", "SENTINEL"}, true},
		{[]string{"--env-file", "SENTINEL", "protocol", "run"}, true},
		{[]string{"--env-file=SENTINEL", "protocol"}, true},
		{[]string{"--engine-socket", "protocol", "version"}, false},
		{[]string{"--engine-socket", "SECRET_SENTINEL", "protocol"}, true},
		{[]string{"-vv", "protocol"}, true}, {[]string{"--unknown", "SENTINEL", "protocol"}, true},
		{[]string{"exec", "web", "protocol"}, false}, {[]string{"-f", "protocol", "version"}, false},
		{[]string{"--env-file", "SENTINEL", "exec", "web", "protocol"}, false},
	} {
		if got := isProtocolInvocation(tt.args); got != tt.want {
			t.Fatalf("%v: %v", tt.args, got)
		}
	}
}

func TestProtocolBoundarySpies(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("socket transport unsupported on this platform")
	}
	oldConfig, oldEngine := configLoader, engineFactory
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		t.Fatal("protocol loaded config")
		return nil, nil
	}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("protocol opened engine")
		return nil, nil
	}
	defer func() { configLoader, engineFactory = oldConfig, oldEngine }()
	path := filepath.Join(t.TempDir(), "SENTINEL-env")
	if err := os.WriteFile(path, []byte("DILL_PROTOCOL_ENV_SENTINEL=SECRET_SENTINEL\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DILL_PROTOCOL_ENV_SENTINEL", "") // loadEnvFile skips existing values; unset below deliberately.
	os.Unsetenv("DILL_PROTOCOL_ENV_SENTINEL")
	t.Setenv("DILL_PKL_PATH", filepath.Join(t.TempDir(), "SENTINEL-missing-pkl"))
	t.Setenv("DILL_ENGINE_SOCKET", "tcp://user:SECRET_SENTINEL@invalid-target")
	for _, args := range [][]string{
		{"protocol"}, {"protocol", "run"},
		{"--env-file", path, "protocol", "run"}, {"protocol", "run", "--env-file", path},
		{"-f", "SECRET_SENTINEL", "protocol"}, {"--engine=SECRET_SENTINEL", "protocol"},
		{"--engine-socket", "SECRET_SENTINEL", "protocol"}, {"--engine-socket=SECRET_SENTINEL", "protocol"},
		{"-vv", "protocol"}, {"protocol", "SECRET_SENTINEL"},
	} {
		out, stderr, err := captureProtocolCommand(t, `{"protocolVersion":"dill.dev/1","operation":"protocol.check","requiredFeatures":[]}`, func() error { return run(args) })
		want := 2
		if len(args) == 1 || (len(args) == 2 && args[1] == "run") {
			want = 0
		}
		if runtime.GOOS == "darwin" {
			want = 1
		}
		if exitCodeFor(err) != want {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		if stderr != "" || strings.Contains(out, "SECRET_SENTINEL") {
			t.Fatalf("unsafe output %s %s", out, stderr)
		}
		if runtime.GOOS == "darwin" {
			if out != "" {
				t.Fatal("unsupported platform wrote stdout", out)
			}
		} else if _, err := protocol.DecodeStream([]byte(out)); err != nil {
			t.Fatal(err, out)
		}
		if _, set := os.LookupEnv("DILL_PROTOCOL_ENV_SENTINEL"); set {
			t.Fatal("env file loaded")
		}
	}
}
