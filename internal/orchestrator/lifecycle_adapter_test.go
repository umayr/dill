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

func TestVolumeRemovalRechecksOwnership(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, tc := range []struct {
			name, labels                string
			inspectStatus, deleteStatus int
			wantDelete, wantErr         bool
		}{
			{"owned retained", `{"dill.managed":"true","dill.stack":"stack"}`, 200, 204, false, true},
			{"absent", ``, 404, 204, false, false},
			{"foreign", `{"dill.managed":"true","dill.stack":"other"}`, 200, 204, false, true},
			{"replaced by unlabelled", `{}`, 200, 204, false, true},
			{"inspect failure", ``, 500, 204, false, true},
			{"owned retained even with force", `{"dill.managed":"true","dill.stack":"stack"}`, 200, 500, false, true},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				var calls []string
				transport := contextTransport(func(r *http.Request) (*http.Response, error) {
					calls = append(calls, r.Method+" "+r.URL.Path)
					if r.Method == http.MethodGet {
						if strings.HasSuffix(r.URL.Path, "/volumes/json") || strings.HasSuffix(r.URL.Path, "/volumes") {
							body := `{"Volumes":[{"Name":"vol","Labels":{"dill.managed":"true","dill.stack":"stack"}}]}`
							if adapter == "podman" {
								body = `[{"Name":"vol","Labels":{"dill.managed":"true","dill.stack":"stack"}}]`
							}
							return contextResponse(r, 200, body), nil
						}
						if tc.inspectStatus != 200 {
							return contextResponse(r, tc.inspectStatus, fmt.Sprintf(`{"message":"inspect injected","response":%d}`, tc.inspectStatus)), nil
						}
						return contextResponse(r, 200, `{"Name":"vol","Labels":`+tc.labels+`}`), nil
					}
					return contextResponse(r, tc.deleteStatus, fmt.Sprintf(`{"message":"delete injected","response":%d}`, tc.deleteStatus)), nil
				})
				var engine Engine
				if adapter == "docker" {
					cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
					if err != nil {
						t.Fatal(err)
					}
					defer cli.Close()
					engine = &DockerEngine{cli: cli}
				} else {
					p, conn := newContextTestEngine(t, context.Background())
					conn.Client.Transport = transport
					engine = p
				}
				listed, err := engine.ListStackVolumes(context.Background(), "stack")
				if err != nil || len(listed) != 1 || listed[0] != "vol" {
					t.Fatalf("list %v: %v", listed, err)
				}
				err = engine.RemoveVolume(context.Background(), "vol", "stack", tc.name == "owned retained even with force")
				if (err != nil) != tc.wantErr {
					t.Fatalf("error %v calls %v", err, calls)
				}
				if tc.wantErr && !strings.Contains(err.Error(), "vol") {
					t.Fatalf("missing volume identity: %v", err)
				}
				deleted := false
				for _, call := range calls {
					if strings.HasPrefix(call, "DELETE ") {
						deleted = true
					}
				}
				if deleted != tc.wantDelete {
					t.Fatalf("calls %v", calls)
				}
			})
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

func TestCanceledCreateReportsUnknownOutcomeWithoutNameCleanup(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		t.Run(adapter, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var deletes []string
			transport := contextTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodDelete {
					deletes = append(deletes, r.URL.Path)
					return contextResponse(r, 204, ""), nil
				}
				if strings.HasSuffix(r.URL.Path, "/containers/create") {
					cancel()
					return nil, context.Canceled
				}
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				return nil, nil
			})
			var engine Engine
			if adapter == "docker" {
				cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				engine = &DockerEngine{cli: cli}
			} else {
				p, conn := newContextTestEngine(t, context.Background())
				conn.Client.Transport = transport
				engine = p
			}
			_, err := engine.StartService(ctx, "web", &config.Service{Image: "busybox"}, "stack")
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "stack_web") {
				t.Fatalf("create outcome not reported: %v", err)
			}
			if len(deletes) != 0 {
				t.Fatalf("unsafe deletion without created ID: %v", deletes)
			}
		})
	}
}

func TestResourceErrorUnwrap(t *testing.T) {
	x := errors.New("failure")
	err := &ResourceError{Operation: "remove", Resource: "x", ID: "id", Err: x}
	if !errors.Is(err, x) {
		t.Fatal(err)
	}
}

func TestLocalImageIDRejectsEmptyInspectID(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, tc := range []struct {
			name, body string
			wantErr    bool
		}{
			{"empty ID", `{}`, true},
			{"valid ID", `{"Id":"sha256:immutable"}`, false},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				transport := contextTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/exists") {
						return contextResponse(r, 204, ""), nil
					}
					return contextResponse(r, 200, tc.body), nil
				})
				var engine Engine
				if adapter == "docker" {
					cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
					if err != nil {
						t.Fatal(err)
					}
					defer cli.Close()
					engine = &DockerEngine{cli: cli}
				} else {
					p, conn := newContextTestEngine(t, context.Background())
					conn.Client.Transport = transport
					engine = p
				}
				id, err := engine.LocalImageID(context.Background(), "example:tag")
				if tc.wantErr {
					if err == nil || !strings.Contains(err.Error(), "example:tag") || !strings.Contains(err.Error(), "retained") {
						t.Fatalf("id=%q error=%v", id, err)
					}
				} else if err != nil || id != "sha256:immutable" {
					t.Fatalf("id=%q error=%v", id, err)
				}
			})
		}
	}
}

func TestLocalImageIDAbsentAndInspectFailure(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
			t.Run(adapter+"/"+http.StatusText(status), func(t *testing.T) {
				transport := contextTransport(func(r *http.Request) (*http.Response, error) {
					return contextResponse(r, status, fmt.Sprintf(`{"message":"injected","response":%d}`, status)), nil
				})
				var engine Engine
				if adapter == "docker" {
					cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
					if err != nil {
						t.Fatal(err)
					}
					defer cli.Close()
					engine = &DockerEngine{cli: cli}
				} else {
					p, conn := newContextTestEngine(t, context.Background())
					conn.Client.Transport = transport
					engine = p
				}
				id, err := engine.LocalImageID(context.Background(), "example:tag")
				if id != "" || (err != nil) != (status != http.StatusNotFound) {
					t.Fatalf("id=%q error=%v", id, err)
				}
			})
		}
	}
}
