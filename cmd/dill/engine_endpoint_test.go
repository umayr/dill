package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestLocalEngineSocketSelection(t *testing.T) {
	t.Setenv("DILL_ENGINE_SOCKET", "unix:///environment.sock")
	for _, tc := range []struct {
		name string
		opts commandOptions
		want string
	}{
		{"environment", commandOptions{}, "unix:///environment.sock"},
		{"flag", commandOptions{EngineSocket: "/flag.sock", EngineSocketSet: true}, "unix:///flag.sock"},
		{"uri", commandOptions{EngineSocket: "unix:///flag.sock"}, "unix:///flag.sock"},
		{"spaces", commandOptions{EngineSocket: "/socket directory/engine.sock"}, "unix:///socket directory/engine.sock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := localEngineSocket(tc.opts)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if err := os.Unsetenv("DILL_ENGINE_SOCKET"); err != nil {
		t.Fatal(err)
	}
	if got, err := localEngineSocket(commandOptions{}); got != "" || err != nil {
		t.Fatalf("legacy selection: %q, %v", got, err)
	}
	// Legacy selectors are intentionally not read by the new override parser.
	t.Setenv("DOCKER_HOST", "tcp://legacy.example:2376")
	t.Setenv("PODMAN_SOCKET", "unix:///legacy.sock")
	if got, err := localEngineSocket(commandOptions{}); got != "" || err != nil {
		t.Fatalf("legacy selection: %q, %v", got, err)
	}
}

func TestLocalEngineSocketRejectsWithoutEchoOrFallback(t *testing.T) {
	for _, value := range []string{
		"", "relative/SENTINEL", "unix:/SENTINEL", "unix://SENTINEL/socket", "//SENTINEL/socket",
		"tcp://user:SENTINEL@127.0.0.1:2375", "ssh://user:SENTINEL@host", "npipe:////SENTINEL",
		"unix:///socket?token=SENTINEL", "/socket#SENTINEL", "/socket%2fSENTINEL", "/socket\\SENTINEL",
		"/socket\nSENTINEL", "unix:///socket\x00SENTINEL", "/socket\xffSENTINEL", "unix:////SENTINEL",
		"/SENTINEL.sock ", "unix:///SENTINEL.sock ", "/SENTINEL.sock\u00a0", "unix:///SENTINEL.sock\u2003",
	} {
		t.Run("flag-"+value, func(t *testing.T) {
			t.Setenv("DILL_ENGINE_SOCKET", "/fallback.sock")
			got, err := localEngineSocket(commandOptions{EngineSocket: value, EngineSocketSet: true})
			if got != "" || exitCodeFor(err) != 2 || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
		if strings.ContainsRune(value, 0) {
			continue // The OS cannot represent NUL in an environment value.
		}
		t.Run("env-"+value, func(t *testing.T) {
			t.Setenv("DILL_ENGINE_SOCKET", value)
			got, err := localEngineSocket(commandOptions{})
			if got != "" || exitCodeFor(err) != 2 || strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

// Exercise every engine entrypoint, including label-only commands and doctor's
// config-failure fallback. No engine (and thus no mutation) may be opened for an
// invalid explicit endpoint. Valid selection must reach the same factory.
func TestEngineEntrypointsShareSocketSelection(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	stopped := errors.New("factory sentinel")
	var endpoints []string
	engineFactory = func(_ context.Context, _ string, endpoint string) (orchestrator.Engine, error) {
		endpoints = append(endpoints, endpoint)
		return nil, stopped
	}
	t.Setenv("DILL_ENGINE_SOCKET", "/environment.sock")
	commands := [][]string{
		{"up"}, {"down"}, {"teardown", "--yes"}, {"stop"}, {"start"}, {"restart"},
		{"ps"}, {"logs"}, {"pull"}, {"volumes"}, {"exec", "web", "true"},
		{"plan"}, {"diff"}, {"status"}, {"stacks"}, {"ls"}, {"doctor"}, {"rm", "--yes"},
		{"wait"}, {"port", "web"}, {"top", "web"}, {"cp", "web:/x", "/tmp/x"},
	}
	for _, command := range commands {
		for _, position := range []string{"global", "command"} {
			for _, socket := range []string{"/flag.sock", "tcp://user:SENTINEL@host", ""} {
				t.Run(strings.Join(command, "-")+"-"+position+"-"+socket, func(t *testing.T) {
					endpoints = nil
					args := append([]string{"--engine-socket", socket}, command...)
					if position == "command" {
						args = append([]string{command[0], "--engine-socket", socket}, command[1:]...)
					}
					out, stderr, err := captureCommand(t, "", func() error { return run(args) })
					if err == nil || strings.Contains(out+stderr+err.Error(), "SENTINEL") {
						t.Fatalf("unsafe result: %v %s %s", err, out, stderr)
					}
					if socket == "/flag.sock" {
						if !reflect.DeepEqual(endpoints, []string{"unix:///flag.sock"}) {
							t.Fatalf("factory endpoints: %v; err=%v", endpoints, err)
						}
					} else if len(endpoints) != 0 {
						t.Fatalf("invalid endpoint opened engine: %v", endpoints)
					}
				})
			}
		}
	}
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return nil, errors.New("invalid config") }
	endpoints = nil
	_, _, err := captureCommand(t, "", func() error { return run([]string{"doctor", "--engine-socket", "/fallback.sock", "--format", "json"}) })
	if !errors.Is(err, errDoctorFoundIssues) || !reflect.DeepEqual(endpoints, []string{"unix:///fallback.sock"}) {
		t.Fatalf("doctor fallback: %v, %v", endpoints, err)
	}
}

func TestInvalidSocketPreservesPlanDiffJSONFailure(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("invalid endpoint opened engine")
		return nil, nil
	}
	for _, command := range []string{"plan", "diff"} {
		out, stderr, err := captureCommand(t, "", func() error {
			return run([]string{command, "--format", "json", "--engine-socket", "tcp://user:SENTINEL@host"})
		})
		var result map[string]any
		if exitCodeFor(err) != 2 || json.Unmarshal([]byte(out), &result) != nil || result["kind"] != "invalid_input" || strings.Contains(out+stderr, "SENTINEL") {
			t.Fatalf("%s: error=%v stdout=%q stderr=%q", command, err, out, stderr)
		}
	}
}

func TestEngineSocketCommandFlagOverridesGlobalAndEnvironment(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	var got string
	engineFactory = func(_ context.Context, _ string, endpoint string) (orchestrator.Engine, error) {
		got = endpoint
		return nil, errors.New("stop before engine access")
	}
	t.Setenv("DILL_ENGINE_SOCKET", "/environment.sock")
	for _, args := range [][]string{
		{"--engine-socket", "/global.sock", "ps", "--engine-socket", "/command.sock"},
		{"ps"},
	} {
		_, _, _ = captureCommand(t, "", func() error { return run(args) })
		want := "unix:///command.sock"
		if len(args) == 1 {
			want = "unix:///environment.sock"
		}
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}
