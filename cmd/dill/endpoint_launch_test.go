package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type endpointFake struct {
	*fakeEngine
	inspected []string
	foreign   bool
}

func (*endpointFake) CLIEndpoint() string { return "unix:///test/selected.sock" }
func (e *endpointFake) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.inspected = append(e.inspected, name)
	labels := map[string]string{"dill.managed": "true", "dill.stack": "stack", "dill.service": "app"}
	if e.foreign {
		labels["dill.stack"] = "foreign"
	}
	return &orchestrator.LiveConfig{ID: "immutable-id", SystemLabels: labels}, nil
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
		return &config.DillConfig{Name: "stack", Engine: "podman", Services: map[string]*config.Service{"app": {Image: "image", ContainerName: "custom-app"}}}, nil
	}
	fake := &endpointFake{fakeEngine: newFakeEngine()}
	fake.statuses["app"] = &orchestrator.ContainerStatus{Name: "app"} // foreign bare name must never be selected
	engineFactory = func(context.Context, string) (orchestrator.Engine, error) { return fake, nil }
	tests := []struct {
		name string
		run  func(context.Context, commandOptions, []string) error
		args []string
		want []string
	}{
		{"exec", runExec, []string{"app", "true"}, []string{"--url", "unix:///test/selected.sock", "exec", "-i", "immutable-id", "true"}},
		{"top", runTop, []string{"app"}, []string{"--url", "unix:///test/selected.sock", "top", "immutable-id"}},
		{"cp", runCp, []string{"app:/a", "/tmp/b"}, []string{"--url", "unix:///test/selected.sock", "cp", "immutable-id:/a", "/tmp/b"}},
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
			if got := fake.inspected[len(fake.inspected)-1]; got != "custom-app" {
				t.Fatalf("inspected %q", got)
			}
		})
	}
	fake.foreign = true
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		if err := tc.run(context.Background(), commandOptions{}, tc.args); !errors.Is(err, orchestrator.ErrOwnershipConflict) {
			t.Fatalf("%s: foreign ownership: %v", tc.name, err)
		}
		if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s launched for foreign container: %v", tc.name, err)
		}
	}
	fake.foreign = false
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return &config.DillConfig{Name: "stack", Engine: "podman", Services: map[string]*config.Service{"app": {Image: "image"}}}, nil
	}
	if err := runExec(context.Background(), commandOptions{}, []string{"app", "true"}); err != nil {
		t.Fatal(err)
	}
	if got := fake.inspected[len(fake.inspected)-1]; got != "stack_app" {
		t.Fatalf("default name: inspected %q", got)
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

func TestDockerCommandsLaunchWithSDKEndpointAndID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.43")
			fmt.Fprint(w, "OK")
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/containers/custom/json") {
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"Id":"immutable-id","Config":{"Image":"image","Labels":{"dill.managed":"true","dill.stack":"stack","dill.service":"app"}},"HostConfig":{},"NetworkSettings":{"Ports":{}}}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	output := filepath.Join(dir, "args")
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_ARGS\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("TEST_ARGS", output)
	endpoint := "tcp://" + server.Listener.Addr().String()
	t.Setenv("DOCKER_HOST", endpoint)
	t.Setenv("DOCKER_CONTEXT", "conflicting-context")
	old := configLoader
	defer func() { configLoader = old }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return &config.DillConfig{Name: "stack", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "image", ContainerName: "custom"}}}, nil
	}
	for _, tc := range []struct {
		name       string
		run        func(context.Context, commandOptions, []string) error
		args, want []string
	}{
		{"exec", runExec, []string{"app", "true"}, []string{"--host", endpoint, "exec", "-i", "immutable-id", "true"}},
		{"top", runTop, []string{"app"}, []string{"--host", endpoint, "top", "immutable-id"}},
		{"cp", runCp, []string{"app:/x", "/tmp/x"}, []string{"--host", endpoint, "cp", "immutable-id:/x", "/tmp/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(context.Background(), commandOptions{}, tc.args); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSpace(string(b)), "\n")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		run  func(context.Context, commandOptions, []string) error
		args []string
	}{
		{"exec", runExec, []string{"app", "true"}}, {"top", runTop, []string{"app"}}, {"cp", runCp, []string{"app:/x", "/tmp/x"}},
	} {
		t.Run("cancel-"+tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := tc.run(ctx, commandOptions{}, tc.args); err == nil || ctx.Err() != context.DeadlineExceeded {
				t.Fatalf("error=%v context=%v", err, ctx.Err())
			}
		})
	}
}

func TestPodmanCommandsLaunchWithSelectedSocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("Libpod-API-Version", "5.0.0")
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/containers/custom/json") {
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"Id":"immutable-id","Config":{"Labels":{"dill.managed":"true","dill.stack":"stack","dill.service":"app"}},"HostConfig":{},"NetworkSettings":{"Ports":{}}}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	output := filepath.Join(dir, "args")
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_ARGS\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("TEST_ARGS", output)
	endpoint := "tcp://" + server.Listener.Addr().String()
	t.Setenv("PODMAN_SOCKET", endpoint)
	t.Setenv("CONTAINER_HOST", "unix:///not-selected.sock")
	t.Setenv("CONTAINER_CONNECTION", "not-selected")
	old := configLoader
	defer func() { configLoader = old }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return &config.DillConfig{Name: "stack", Engine: "podman", Services: map[string]*config.Service{"app": {Image: "image", ContainerName: "custom"}}}, nil
	}
	for _, tc := range []struct {
		name       string
		run        func(context.Context, commandOptions, []string) error
		args, want []string
	}{
		{"exec", runExec, []string{"app", "true"}, []string{"--url", endpoint, "exec", "-i", "immutable-id", "true"}},
		{"top", runTop, []string{"app"}, []string{"--url", endpoint, "top", "immutable-id"}},
		{"cp", runCp, []string{"app:/x", "/tmp/x"}, []string{"--url", endpoint, "cp", "immutable-id:/x", "/tmp/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(context.Background(), commandOptions{}, tc.args); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(output)
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

func TestDataPlaneSubprocessCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte("#!/bin/sh\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader = oldLoader; engineFactory = oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return &config.DillConfig{Name: "stack", Engine: "podman", Services: map[string]*config.Service{"app": {Image: "image"}}}, nil
	}
	engineFactory = func(context.Context, string) (orchestrator.Engine, error) {
		return &endpointFake{fakeEngine: newFakeEngine()}, nil
	}
	for _, tc := range []struct {
		name string
		run  func(context.Context, commandOptions, []string) error
		args []string
	}{{"exec", runExec, []string{"app", "true"}}, {"top", runTop, []string{"app"}}, {"cp", runCp, []string{"app:/x", "/tmp/x"}}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := tc.run(ctx, commandOptions{}, tc.args); err == nil || ctx.Err() != context.DeadlineExceeded {
				t.Fatalf("error=%v ctx=%v", err, ctx.Err())
			}
		})
	}
}
