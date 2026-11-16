package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/core"
)

type fake struct {
	stack     string
	inspected string
	fail      bool
}

func (f *fake) ListStack(_ context.Context, s string) ([]string, error) {
	f.stack = s
	return []string{"one"}, nil
}
func (f *fake) ServiceStatus(_ context.Context, s string) (*core.ContainerStatus, error) {
	f.inspected = s
	if f.fail {
		return nil, errors.New("private engine detail")
	}
	return &core.ContainerStatus{Name: s, State: "running"}, nil
}
func TestAPI(t *testing.T) {
	f := &fake{}
	a := API{Source: f, Stack: "fixed", Engine: "docker"}
	for _, tc := range []struct {
		method, path string
		code         int
		want         string
	}{{"GET", "/v1/health", 200, "capabilities"}, {"GET", "/v1/status", 200, "containers"}, {"GET", "/v1/status?stack=other", 400, "invalid_request"}, {"POST", "/v1/status", 405, "method_not_allowed"}, {"GET", "/v1/plan", 404, "unknown_operation"}} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatal("invalid JSON")
		}
	}
	if f.stack != "fixed" || f.inspected != "one" {
		t.Fatalf("core status not invoked on fixed target: %+v", f)
	}
}
func TestStatusResponseRedactsUnknownAndErrors(t *testing.T) {
	f := &fake{fail: true}
	w := httptest.NewRecorder()
	API{Source: f, Stack: "fixed", Engine: "podman"}.ServeHTTP(w, httptest.NewRequest("GET", "/v1/status", nil))
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("response: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	if _, ok := got["services"]; ok {
		t.Fatal("unknown services present")
	}
	if _, ok := got["config_file"]; ok {
		t.Fatal("unknown config_file present")
	}
	if string(got["stack"]) != `"fixed"` || string(got["engine"]) != `"podman"` {
		t.Fatalf("wrong target: %s", w.Body.String())
	}
	var containers []core.ContainerStatus
	if err := json.Unmarshal(got["containers"], &containers); err != nil || len(containers) != 1 || containers[0].Name != "one" || containers[0].State != "error" || containers[0].Status != "container status unavailable" {
		t.Fatalf("bad containers: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private engine detail") {
		t.Fatal("engine error leaked")
	}
}
func TestEndpoint(t *testing.T) {
	for _, v := range []struct {
		engine, endpoint string
		valid            bool
	}{{"docker", "unix:///tmp/docker.sock", true}, {"podman", "unix:///tmp/podman.sock", true}, {"docker", "tcp://localhost:123", false}, {"docker", "unix:///tmp/a?x=1", false}, {"other", "unix:///tmp/a", false}, {"docker", "unix:///tmp/a\x00b", false}, {"podman", "unix:///tmp/a\nb", false}, {"podman", "unix:///tmp/a\u0085b", false}} {
		if (ValidateEndpoint(v.engine, v.endpoint) == nil) != v.valid {
			t.Fatal(v)
		}
	}
}
func TestSocketAndShutdown(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "api.sock")
	for _, suffix := range []string{"\x00", "\n", "\u0085"} {
		if _, _, err := Listen(path + suffix); err == nil {
			t.Fatal("control character accepted")
		}
	}
	l, cleanup, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	st, err := os.Lstat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("socket mode: %v %v", st, err)
	}
	if _, _, err := Listen(path); err == nil {
		t.Fatal("existing socket accepted")
	}
	s := Server(API{Source: &fake{}, Stack: "fixed", Engine: "docker"})
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	client := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
	resp, err := client.Get("http://unix/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	cleanup()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("socket not removed")
	}
	if err := os.Symlink("missing", path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Listen(path); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Listen(path); err == nil {
		t.Fatal("insecure directory accepted")
	}
}

type deadlineSource struct{ inspected int }

func (*deadlineSource) ListStack(context.Context, string) ([]string, error) {
	return []string{"one", "two"}, nil
}
func (s *deadlineSource) ServiceStatus(ctx context.Context, name string) (*core.ContainerStatus, error) {
	s.inspected++
	<-ctx.Done()
	// An engine may return a non-context error after its context expires.
	return nil, errors.New("private engine detail")
}

func TestStatusHandlerDeadline(t *testing.T) {
	s := &deadlineSource{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/v1/status", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	API{Source: s, Stack: "fixed", Engine: "docker"}.ServeHTTP(w, req)
	if ctx.Err() != context.DeadlineExceeded || w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), `"code":"timeout"`) || strings.Contains(w.Body.String(), "private engine detail") {
		t.Fatalf("context=%v response=%d %s", ctx.Err(), w.Code, w.Body.String())
	}
	if s.inspected != 1 {
		t.Fatalf("inspections=%d", s.inspected)
	}
}

func TestReplyWireSizeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		payloadLength, status int
	}{
		{"exact wire limit", maxResponse - 3, http.StatusOK},
		{"newline exceeds limit", maxResponse - 2, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			reply(w, http.StatusOK, strings.Repeat("x", tc.payloadLength))
			if w.Code != tc.status || w.Body.Len() > maxResponse || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("status=%d length=%d", w.Code, w.Body.Len())
			}
			if tc.status == http.StatusOK && w.Body.Len() != maxResponse {
				t.Fatalf("length=%d", w.Body.Len())
			}
			if tc.status != http.StatusOK && !strings.Contains(w.Body.String(), "response_too_large") {
				t.Fatalf("body=%s", w.Body.String())
			}
		})
	}
}
