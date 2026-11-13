//go:build linux || darwin

package coordination

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testDaemon = "synthetic-daemon-1234"

func bounded() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dill-coord-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
func fakeDocker(t *testing.T, path, id string, requests *atomic.Int32) *http.Server {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			fmt.Fprintf(w, `{"ID":%q}`, id)
			return
		}
		requests.Add(1)
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "http://outside.invalid/private")
			w.WriteHeader(302)
			return
		}
		if r.URL.Path == "/disconnect" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		io.WriteString(w, "synthetic-ok")
	})}
	go srv.Serve(listener)
	t.Cleanup(func() { srv.Close() })
	return srv
}
func qualified(t *testing.T, path string) *Target {
	t.Helper()
	ctx, cancel := bounded()
	defer cancel()
	target, err := Qualify(ctx, Qualification{NativeDockerLayout, path, testDaemon})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.Close() })
	return target
}
func TestTargetSocketAliasesAndReplacement(t *testing.T) {
	dir := socketDir(t)
	path := filepath.Join(dir, "engine.sock")
	var first, second atomic.Int32
	fakeDocker(t, path, testDaemon, &first)
	alias := filepath.Join(dir, "alias.sock")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	a, b := qualified(t, path), qualified(t, alias)
	if a.Identity() != b.Identity() {
		t.Fatal("socket aliases changed identity")
	}
	if err := os.Rename(path, filepath.Join(dir, "old.sock")); err != nil {
		t.Fatal(err)
	}
	fakeDocker(t, path, "replacement-daemon-4321", &second)
	for _, target := range []*Target{a, b} {
		resp, err := target.Client().Get(Endpoint + "/probe")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if first.Load() != 2 || second.Load() != 0 {
		t.Fatal("operation retargeted to replacement socket")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Client().Get(Endpoint + "/probe"); err == nil {
		t.Fatal("reconnected after close")
	}
	if second.Load() != 0 {
		t.Fatal("reconnected to replacement daemon")
	}
	ctx, cancel := bounded()
	defer cancel()
	if _, err := Qualify(ctx, Qualification{NativeDockerLayout, path, testDaemon}); err == nil {
		t.Fatal("accepted unqualified replacement daemon")
	}
}
func TestSameDaemonDifferentSockets(t *testing.T) {
	dir := socketDir(t)
	var calls atomic.Int32
	a, b := filepath.Join(dir, "a.sock"), filepath.Join(dir, "b.sock")
	fakeDocker(t, a, testDaemon, &calls)
	fakeDocker(t, b, testDaemon, &calls)
	if qualified(t, a).Identity() != qualified(t, b).Identity() {
		t.Fatal("actual daemon aliases diverged")
	}
}
func TestTransportFailClosed(t *testing.T) {
	dir := socketDir(t)
	path := filepath.Join(dir, "engine.sock")
	var calls atomic.Int32
	fakeDocker(t, path, testDaemon, &calls)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	target := qualified(t, path)
	if _, err := target.Client().Get(Endpoint + "/redirect"); err == nil {
		t.Fatal("followed redirect")
	}
	if _, err := target.Client().Get("http://outside.invalid/probe"); err == nil {
		t.Fatal("accepted foreign endpoint")
	}
	if _, err := target.Client().Get(Endpoint + "/disconnect"); err == nil {
		t.Fatal("accepted disconnected response")
	}
	if _, err := target.Client().Get(Endpoint + "/probe"); err == nil {
		t.Fatal("silently reconnected")
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected calls: %d", calls.Load())
	}
}
func TestConcurrentHTTPUsesOnlyQualifiedConnection(t *testing.T) {
	dir := socketDir(t)
	path := filepath.Join(dir, "engine.sock")
	var calls atomic.Int32
	fakeDocker(t, path, testDaemon, &calls)
	target := qualified(t, path)
	var workers sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx, cancel := bounded()
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint+"/probe", nil)
			response, err := target.Client().Do(req)
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 16 {
		t.Fatalf("unexpected synthetic requests: %d", calls.Load())
	}
}

func TestRejectUnsupportedTargets(t *testing.T) {
	dir := socketDir(t)
	file := filepath.Join(dir, "ordinary")
	os.WriteFile(file, nil, 0600)
	ctx, cancel := bounded()
	defer cancel()
	for _, q := range []Qualification{{"container", file, testDaemon}, {NativeDockerLayout, "relative", testDaemon}, {NativeDockerLayout, file, testDaemon}, {NativeDockerLayout, file, "secret/path"}} {
		if target, err := Qualify(ctx, q); err == nil {
			target.Close()
			t.Fatal("accepted unsupported target")
		}
	}
	if _, err := Qualify(context.Background(), Qualification{NativeDockerLayout, file, testDaemon}); err == nil {
		t.Fatal("accepted unbounded qualification")
	}
	cancel()
	if _, err := Qualify(ctx, Qualification{NativeDockerLayout, file, testDaemon}); err == nil || errors.Is(err, nil) {
		t.Fatal("accepted cancellation")
	}
}
