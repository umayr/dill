package orchestrator

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/umayr/dill/internal/config"
)

type contextTransport func(*http.Request) (*http.Response, error)

func (f contextTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newContextTestEngine(t *testing.T, parent context.Context) (*PodmanEngine, *bindings.Connection) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			t.Errorf("unexpected handshake request %s", r.URL.Path)
		}
		w.Header().Set("Libpod-API-Version", "5.0.0")
	}))
	t.Cleanup(server.Close)
	engine, err := NewPodmanEngine(parent, "tcp://"+server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := bindings.GetClient(engine.conn)
	if err != nil {
		t.Fatal(err)
	}
	conn.Client.CloseIdleConnections()
	return engine, conn
}

func contextResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestPodmanOperationContext(t *testing.T) {
	type requestKey struct{}
	parent, cancelParent := context.WithCancel(context.Background())
	engine, conn := newContextTestEngine(t, parent)
	cancelParent()

	operation, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, "request-value"), time.Minute)
	defer cancel()
	ctx := engine.operationContext(operation)
	if got, _ := bindings.GetClient(ctx); got != conn {
		t.Fatal("SDK connection was not preserved")
	}
	if got := bindings.ServiceVersion(ctx).String(); got != "5.0.0" {
		t.Fatalf("SDK version = %s", got)
	}
	if got := ctx.Value(requestKey{}); got != "request-value" {
		t.Fatalf("request value = %v", got)
	}
	wantDeadline, _ := operation.Deadline()
	if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(wantDeadline) {
		t.Fatal("operation deadline lost")
	}
	if ctx.Err() != nil {
		t.Fatalf("connection cancellation leaked into operation: %v", ctx.Err())
	}
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("operation cancellation lost")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("Err = %v", ctx.Err())
	}

	// Fresh cleanup must still use the established client after its original
	// initialization/deployment context was cancelled.
	conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		return contextResponse(r, http.StatusNoContent, ""), nil
	})
	if err := engine.StopService(context.Background(), "created-id"); err != nil {
		t.Fatalf("cleanup failed after parent cancellation: %v", err)
	}
}

func TestPodmanOperationsCancelInflightRequests(t *testing.T) {
	for _, tc := range []struct {
		name  string
		allow string // optional successful first step of a multi-request operation
		call  func(context.Context, *PodmanEngine) error
	}{
		{"pull", "", func(ctx context.Context, e *PodmanEngine) error { return e.PullImage(ctx, "busybox", io.Discard) }},
		{"image exists", "", func(ctx context.Context, e *PodmanEngine) error { _, err := e.ImageExists(ctx, "image"); return err }},
		{"local image inspect", "", func(ctx context.Context, e *PodmanEngine) error { _, err := e.LocalImageID(ctx, "image"); return err }},
		{"create", "", func(ctx context.Context, e *PodmanEngine) error {
			_, err := e.StartService(ctx, "web", &config.Service{Image: "busybox"}, "app")
			return err
		}},
		{"start after create", "/containers/create", func(ctx context.Context, e *PodmanEngine) error {
			_, err := e.StartService(ctx, "web", &config.Service{Image: "busybox"}, "app")
			return err
		}},
		{"stop", "", func(ctx context.Context, e *PodmanEngine) error { return e.StopService(ctx, "container") }},
		{"remove", "", func(ctx context.Context, e *PodmanEngine) error { return e.RemoveService(ctx, "container") }},
		{"readiness", "", func(ctx context.Context, e *PodmanEngine) error {
			_, err := e.IsReady(ctx, "container", false)
			return err
		}},
		{"list stack", "", func(ctx context.Context, e *PodmanEngine) error { _, err := e.ListStack(ctx, "app"); return err }},
		{"list stacks", "", func(ctx context.Context, e *PodmanEngine) error { _, err := e.ListStacks(ctx); return err }},
		{"start existing", "", func(ctx context.Context, e *PodmanEngine) error { return e.StartExisting(ctx, "container") }},
		{"status", "", func(ctx context.Context, e *PodmanEngine) error {
			_, err := e.ServiceStatus(ctx, "container")
			return err
		}},
		{"logs", "", func(ctx context.Context, e *PodmanEngine) error {
			return e.Logs(ctx, "container", LogOptions{Follow: true}, io.Discard)
		}},
		{"inspect", "", func(ctx context.Context, e *PodmanEngine) error {
			_, err := e.InspectConfig(ctx, "container")
			return err
		}},
		{"network exists", "", func(ctx context.Context, e *PodmanEngine) error { return e.EnsureNetwork(ctx, "network") }},
		{"network create", "/networks/network/exists", func(ctx context.Context, e *PodmanEngine) error { return e.EnsureNetwork(ctx, "network") }},
		{"network remove", "", func(ctx context.Context, e *PodmanEngine) error { return e.RemoveNetwork(ctx, "network") }},
		{"image remove", "", func(ctx context.Context, e *PodmanEngine) error { return e.RemoveImage(ctx, "image", false) }},
		{"volume exists", "", func(ctx context.Context, e *PodmanEngine) error { return e.EnsureVolume(ctx, "volume", "app") }},
		{"volume create", "/volumes/volume/exists", func(ctx context.Context, e *PodmanEngine) error { return e.EnsureVolume(ctx, "volume", "app") }},
		{"volume list", "", func(ctx context.Context, e *PodmanEngine) error { _, err := e.ListStackVolumes(ctx, "app"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, conn := newContextTestEngine(t, context.Background())
			entered := make(chan struct{}, 1)
			cleaned := make(chan struct{}, 1)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/containers/created-container-id") {
					if err := r.Context().Err(); err != nil {
						t.Errorf("failed-start cleanup inherited cancellation: %v", err)
					}
					if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) <= 0 || time.Until(deadline) > failedStartCleanupTimeout {
						t.Error("failed-start cleanup lost its independent deadline")
					}
					cleaned <- struct{}{}
					return contextResponse(r, http.StatusOK, "[]"), nil
				}
				if tc.allow != "" && strings.HasSuffix(r.URL.Path, tc.allow) {
					if strings.HasPrefix(tc.allow, "/networks/") || strings.HasPrefix(tc.allow, "/volumes/") {
						return contextResponse(r, http.StatusNotFound, ""), nil
					}
					return contextResponse(r, http.StatusOK, `{"Id":"created-container-id"}`), nil
				}
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-r.Context().Done():
					return nil, r.Context().Err()
				case <-release:
					return nil, errors.New("test request released")
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- tc.call(ctx, engine) }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("operation ended before blocking: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("operation did not reach transport")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("operation error = %v, want cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("in-flight request ignored cancellation")
			}
			if tc.allow == "/containers/create" {
				select {
				case <-cleaned:
				default:
					t.Fatal("cancelled start did not clean up its created container")
				}
			}
		})
	}
}

func TestPodmanRequestDeadline(t *testing.T) {
	engine, conn := newContextTestEngine(t, context.Background())
	release := make(chan struct{})
	defer close(release)
	conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
			return nil, errors.New("test request released")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := engine.ImageExists(ctx, "image"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want deadline exceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request ignored deadline")
	}
}

type contextBody struct {
	context.Context
	release <-chan struct{}
}

func (b contextBody) Read([]byte) (int, error) {
	select {
	case <-b.Done():
		return 0, b.Err()
	case <-b.release:
		return 0, io.EOF
	}
}
func (contextBody) Close() error { return nil }

func TestPodmanStreamingCancellation(t *testing.T) {
	for _, name := range []string{"pull", "logs"} {
		t.Run(name, func(t *testing.T) {
			engine, conn := newContextTestEngine(t, context.Background())
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
				response := contextResponse(r, http.StatusOK, "")
				response.Body = contextBody{Context: r.Context(), release: release}
				close(entered)
				return response, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if name == "pull" {
					done <- engine.PullImage(ctx, "image", io.Discard)
				} else {
					done <- engine.Logs(ctx, "container", LogOptions{Follow: true}, io.Discard)
				}
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("stream ended before response: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not reach transport")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("stream error = %v, want cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stream ignored cancellation")
			}
		})
	}
}
