//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
)

func fakeUnixEngine(t *testing.T, handler http.Handler) string {
	t.Helper()
	// Keep below Darwin's 104-byte Unix socket path limit, even with long test
	// names. This is a disposable HTTP fake, never a container-engine socket.
	dir, err := os.MkdirTemp("", "dill-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "engine.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return "unix://" + path
}

func TestDockerCLITrimsSocketHostWhitespace(t *testing.T) {
	binary, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker CLI not installed")
	}
	var hits atomic.Int32
	endpoint := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("API-Version", "1.43")
		fmt.Fprint(w, "OK")
	}))
	for _, suffix := range []string{" ", "\u00a0", "\u2003"} {
		hits.Store(0)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, binary, "--host", endpoint+suffix, "version")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
		output, err := cmd.CombinedOutput()
		cancel()
		if hits.Load() == 0 {
			t.Fatalf("Docker CLI did not reach trimmed host for %q: %v: %s", suffix, err, output)
		}
		if got, err := localEngineSocket(commandOptions{EngineSocket: endpoint + suffix, EngineSocketSet: true}); got != "" || exitCodeFor(err) != 2 {
			t.Fatalf("accepted CLI-normalized socket: %q, %v", got, err)
		}
	}
}

func TestMissingExplicitSocketDoesNotFallback(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			var wrongHits atomic.Int32
			wrong := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wrongHits.Add(1)
				fmt.Fprint(w, `[]`)
			}))
			t.Setenv("DOCKER_HOST", wrong)
			t.Setenv("PODMAN_SOCKET", wrong)
			t.Setenv("DILL_ENGINE_SOCKET", wrong)
			_, _, err := captureCommand(t, "", func() error {
				return run([]string{"stacks", "--engine", engine, "--engine-socket", t.TempDir() + "/missing.sock"})
			})
			if err == nil || wrongHits.Load() != 0 {
				t.Fatalf("error=%v wrong target requests=%d", err, wrongHits.Load())
			}
		})
	}
}

func TestExplicitSocketAcquisitionRespectsCancellation(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			var requests atomic.Int32
			selected := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				<-r.Context().Done()
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, _, err := captureCommand(t, "", func() error {
				return runStacks(ctx, commandOptions{}, []string{"--engine", engine, "--engine-socket", selected})
			})
			if err == nil || ctx.Err() == nil || time.Since(start) > 2*time.Second || requests.Load() == 0 {
				t.Fatalf("error=%v context=%v elapsed=%s requests=%d", err, ctx.Err(), time.Since(start), requests.Load())
			}
		})
	}
}

func TestExplicitSocketPinsSDKAndSubprocesses(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			var wrongHits atomic.Int32
			wrong := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wrongHits.Add(1)
				http.Error(w, "wrong engine selected", 500)
			}))
			var mu sync.Mutex
			var paths []string
			selected := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.Method+" "+r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/_ping"):
					w.Header().Set("API-Version", "1.43")
					w.Header().Set("Libpod-API-Version", "5.0.0")
					fmt.Fprint(w, "OK")
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					fmt.Fprint(w, `[]`)
				case strings.HasSuffix(r.URL.Path, "/containers/custom/json"):
					fmt.Fprint(w, `{"Id":"immutable-id","Name":"/custom","Config":{"Image":"image","Labels":{"dill.managed":"true","dill.stack":"stack","dill.service":"app"}},"State":{"Running":true,"Status":"running"},"HostConfig":{},"NetworkSettings":{"Ports":{}}}`)
				case strings.HasSuffix(r.URL.Path, "/containers/app/json"):
					w.WriteHeader(404)
					fmt.Fprint(w, `{"message":"no such container","response":404}`)
				case strings.HasSuffix(r.URL.Path, "/containers/immutable-id/stop"):
					w.WriteHeader(204)
				case strings.HasSuffix(r.URL.Path, "/containers/custom/logs"):
					// Empty multiplexed log stream is valid for both engines.
				default:
					t.Errorf("unexpected fake-engine request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			// Deliberately conflicting defaults must never receive a request.
			for _, key := range []string{"DILL_ENGINE_SOCKET", "DOCKER_HOST", "PODMAN_SOCKET", "CONTAINER_HOST"} {
				t.Setenv(key, wrong)
			}
			t.Setenv("DOCKER_CONTEXT", "wrong-context")
			t.Setenv("CONTAINER_CONNECTION", "wrong-connection")
			t.Setenv("CONTAINER_SSHKEY", "/missing/ssh-key")
			t.Setenv("DOCKER_TLS_VERIFY", "1")
			t.Setenv("DOCKER_TLS", "1")
			t.Setenv("DOCKER_CERT_PATH", "/missing/certificates")
			t.Setenv("DOCKER_API_VERSION", "invalid-ambient-version")
			oldLoader := configLoader
			defer func() { configLoader = oldLoader }()
			configLoader = func(context.Context, string) (*config.DillConfig, error) {
				return &config.DillConfig{Name: "stack", Engine: engine, Services: map[string]*config.Service{"app": {Image: "image", ContainerName: "custom"}}}, nil
			}
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			// Verify real inherited environment as well as argv; a --host flag
			// alone is insufficient when a CLI has ambient context/TLS selectors.
			script := `#!/bin/sh
for key in DOCKER_HOST DOCKER_CONTEXT DOCKER_TLS DOCKER_TLS_VERIFY DOCKER_CERT_PATH DOCKER_API_VERSION CONTAINER_HOST CONTAINER_CONNECTION CONTAINER_SSHKEY PODMAN_SOCKET; do
  eval 'present=${'"$key"'+set}'
  [ "$present" != set ] || exit 73
done
[ "$PRESERVED_TEST_VALUE" = preserved ] || exit 74
printf '%s\n' "$@" > "$TEST_ARGS"
`
			if err := os.WriteFile(filepath.Join(dir, engine), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
			t.Setenv("TEST_ARGS", argsFile)
			t.Setenv("PRESERVED_TEST_VALUE", "preserved")
			for _, args := range [][]string{{"ps", "--format", "json"}, {"logs", "app"}, {"stop", "--quiet", "app"}} {
				_, _, err := captureCommand(t, "", func() error {
					return run(append([]string{"--engine-socket", selected}, args...))
				})
				if err != nil {
					t.Fatalf("%v: %v", args, err)
				}
			}
			endpointFlag := "--host"
			if engine == "podman" {
				endpointFlag = "--url"
			}
			for _, tc := range []struct{ args, want []string }{
				{[]string{"exec", "app", "true"}, []string{"exec", "-i", "immutable-id", "true"}},
				{[]string{"top", "app"}, []string{"top", "immutable-id"}},
				{[]string{"cp", "app:/x", "/tmp/x"}, []string{"cp", "immutable-id:/x", "/tmp/x"}},
			} {
				_, _, err := captureCommand(t, "", func() error {
					return run(append([]string{"--engine-socket", selected}, tc.args...))
				})
				if err != nil {
					t.Fatalf("%v: %v", tc.args, err)
				}
				b, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatal(err)
				}
				want := append([]string{endpointFlag, selected}, tc.want...)
				if got := strings.Split(strings.TrimSpace(string(b)), "\n"); !reflect.DeepEqual(got, want) {
					t.Fatalf("argv=%q, want %q", got, want)
				}
			}
			if wrongHits.Load() != 0 {
				t.Fatalf("wrong default engine received %d requests", wrongHits.Load())
			}
			mu.Lock()
			allPaths := strings.Join(paths, "\n")
			mu.Unlock()
			for _, suffix := range []string{"containers/json", "containers/custom/logs", "containers/immutable-id/stop", "containers/custom/json"} {
				if !strings.Contains(allPaths, suffix) {
					t.Fatalf("missing %s request in %s", suffix, allPaths)
				}
			}
		})
	}
}
