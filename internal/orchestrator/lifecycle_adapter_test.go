package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"

	"github.com/docker/docker/client"
)

func TestDockerLifecycleNetworkOwnershipAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name, labels        string
		deleteStatus        int
		wantDelete, wantErr bool
	}{
		{"foreign", `{"dill.stack":"stack"}`, 204, false, true},
		{"owned deletion failure", `{"dill.managed":"true","dill.stack":"stack"}`, 500, true, true},
		{"owned", `{"dill.managed":"true","dill.stack":"stack"}`, 204, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var target string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					io.WriteString(w, `{"Id":"immutable-network-id","Name":"stack","Labels":`+tc.labels+`}`)
					return
				}
				target = r.URL.Path
				w.WriteHeader(tc.deleteStatus)
				if tc.deleteStatus >= 400 {
					io.WriteString(w, `{"message":"injected"}`)
				}
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.44"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			err = (&DockerEngine{cli: cli}).RemoveNetwork(context.Background(), "stack")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err %v", err)
			}
			if (target != "") != tc.wantDelete {
				t.Fatalf("target %q", target)
			}
			if target != "" && !strings.HasSuffix(target, "/immutable-network-id") {
				t.Fatalf("unsafe target %s", target)
			}
		})
	}
}
func TestPodmanLifecycleNetworkOwnershipAndFailure(t *testing.T) {
	for _, foreign := range []bool{true, false} {
		t.Run(map[bool]string{true: "foreign", false: "remove failure"}[foreign], func(t *testing.T) {
			e, conn := newContextTestEngine(t, context.Background())
			var target string
			conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					labels := `{"dill.managed":"true","dill.stack":"stack"}`
					if foreign {
						labels = `{}`
					}
					return contextResponse(r, 200, `{"id":"network-id","name":"stack","labels":`+labels+`}`), nil
				}
				target = r.URL.Path
				return contextResponse(r, 500, `{"message":"injected","response":500}`), nil
			})
			err := e.RemoveNetwork(context.Background(), "stack")
			if err == nil {
				t.Fatal("expected error")
			}
			if foreign && target != "" {
				t.Fatalf("removed foreign: %s", target)
			}
			if !foreign && !strings.HasSuffix(target, "/network-id") {
				t.Fatalf("target %q err %v", target, err)
			}
		})
	}
}
func TestAdapterContainerRemovalFailures(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := `{"message":"injected","response":` + fmt.Sprint(status) + `}`
			transport := contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, status, body), nil })
			cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			p, conn := newContextTestEngine(t, context.Background())
			conn.Client.Transport = transport
			for _, engine := range []Engine{&DockerEngine{cli: cli}, p} {
				for _, op := range []func(context.Context, string) error{engine.StopService, engine.RemoveService} {
					err := op(context.Background(), "immutable-id")
					if (err != nil) != (status != http.StatusNotFound) {
						t.Errorf("%T status %d: %v", engine, status, err)
					}
				}
			}
		})
	}
}

func TestVolumeRemovalExplicitlyUnsupported(t *testing.T) {
	engines := []Engine{&DockerEngine{}, &PodmanEngine{}}
	for _, e := range engines {
		err := e.RemoveVolume(context.Background(), "protected", false)
		if err == nil || !strings.Contains(err.Error(), "protected") || !strings.Contains(err.Error(), "retained") {
			t.Fatalf("%T: %v", e, err)
		}
	}
}
func TestDockerCanceledStartCleanupUsesCreatedID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var removed string
	transport := contextTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			return contextResponse(r, 201, `{"Id":"created-immutable-id"}`), nil
		case strings.HasSuffix(r.URL.Path, "/start"):
			cancel()
			return nil, context.Canceled
		case r.Method == http.MethodDelete:
			removed = r.URL.Path
			if r.Context().Err() != nil {
				t.Error("cleanup inherited cancellation")
			}
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Minute {
				t.Error("cleanup missing bounded deadline")
			}
			return contextResponse(r, 500, `{"message":"cleanup denied"}`), nil
		default:
			t.Fatalf("unexpected request %s", r.URL)
			return nil, nil
		}
	})
	cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&DockerEngine{cli: cli}).StartService(ctx, "web", &config.Service{Image: "busybox"}, "stack")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "cleanup denied") || !strings.Contains(removed, "created-immutable-id") {
		t.Fatalf("err %v, removed %q", err, removed)
	}
}

func TestResourceErrorUnwrap(t *testing.T) {
	x := errors.New("failure")
	err := &ResourceError{Operation: "remove", Resource: "x", ID: "id", Err: x}
	if !errors.Is(err, x) {
		t.Fatal(err)
	}
}
