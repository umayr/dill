package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type endpointFake struct{ *fakeEngine }

func (*endpointFake) CLIEndpoint() string { return "unix:///test/selected.sock" }

func TestUpCapabilityRejectionBeforeEngineOpen(t *testing.T) {
	cases := []struct {
		name    string
		service *config.Service
		want    string
	}{
		{"device", &config.Service{Image: "image", PullPolicy: "always", Devices: []string{"/dev/a"}}, "device"},
		{"network", &config.Service{Image: "image", PullPolicy: "always", NetworkMode: "container:other"}, "network mode"},
		{"port", &config.Service{Image: "image", PullPolicy: "always", Ports: []json.RawMessage{json.RawMessage(`"80"`)}}, "unpublished port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := false
			cfg := &config.DillConfig{Engine: "podman", Name: "stack", Services: map[string]*config.Service{"app": tc.service}}
			err := runUpConfig(context.Background(), commandOptions{}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) {
				opened = true
				return newFakeEngine(), nil
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) || opened {
				t.Fatalf("error=%v opened=%v", err, opened)
			}
		})
	}
}

func TestCommandsUseAdapterEndpoint(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("TEST_ARGS", log)
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader = oldLoader; engineFactory = oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return &config.DillConfig{Name: "stack", Engine: "podman", Services: map[string]*config.Service{"app": {Image: "image"}}}, nil
	}
	engineFactory = func(context.Context, string) (orchestrator.Engine, error) { return &endpointFake{newFakeEngine()}, nil }
	tests := []struct {
		name string
		run  func(context.Context, commandOptions, []string) error
		args []string
		want []string
	}{
		{"exec", runExec, []string{"app", "true"}, []string{"--url", "unix:///test/selected.sock", "exec", "-i", "stack_app", "true"}},
		{"top", runTop, []string{"app"}, []string{"--url", "unix:///test/selected.sock", "top", "stack_app"}},
		{"cp", runCp, []string{"app:/a", "/tmp/b"}, []string{"--url", "unix:///test/selected.sock", "cp", "stack_app:/a", "/tmp/b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(context.Background(), commandOptions{}, tc.args); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSpace(string(b)), "\n")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDockerCommandUsesSDKHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:54321")
	d, err := orchestrator.NewDockerEngine()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	binary, args := engineCommand(d, "top", "app")
	if binary != "docker" || !reflect.DeepEqual(args, []string{"--host", "tcp://127.0.0.1:54321", "top", "app"}) {
		t.Fatalf("%s %v", binary, args)
	}
}
