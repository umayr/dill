package orchestrator

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	timetypes "github.com/docker/docker/api/types/time"
	"github.com/docker/docker/client"
)

func TestAdapterMissingInspect(t *testing.T) {
	p, conn := newContextTestEngine(t, context.Background())
	conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
		return contextResponse(r, 404, `{"cause":"no such container","message":"missing","response":404}`), nil
	})
	for _, f := range []func() error{
		func() error { _, e := p.InspectConfig(context.Background(), "missing"); return e },
		func() error { _, e := p.ServiceStatus(context.Background(), "missing"); return e },
		func() error { return p.StartExisting(context.Background(), "missing") },
	} {
		if err := f(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("podman missing: %v", err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		io.WriteString(w, `{"message":"missing"}`)
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.43"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	d := &DockerEngine{cli: cli}
	if _, err := d.InspectConfig(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("docker missing: %v", err)
	}
	if _, err := d.ServiceStatus(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("docker status missing: %v", err)
	}
}

func TestAdapterStartExistingWireResponses(t *testing.T) {
	transportFailure := errors.New("injected transport failure")
	for _, adapter := range []string{"docker", "podman"} {
		for _, tc := range []struct {
			name         string
			status       int
			transportErr error
			wantErr      bool
		}{
			{"started", http.StatusNoContent, nil, false},
			{"already running", http.StatusNotModified, nil, false},
			{"permission denied", http.StatusForbidden, nil, true},
			{"conflict", http.StatusConflict, nil, true},
			{"server failure", http.StatusInternalServerError, nil, true},
			{"transport failure", 0, transportFailure, true},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				var paths []string
				transport := contextTransport(func(r *http.Request) (*http.Response, error) {
					paths = append(paths, r.Method+" "+r.URL.Path)
					if tc.transportErr != nil {
						return nil, tc.transportErr
					}
					return contextResponse(r, tc.status, `{"message":"injected failure","response":`+strconv.Itoa(tc.status)+`}`), nil
				})
				var engine Engine
				if adapter == "docker" {
					cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.43"), client.WithHTTPClient(&http.Client{Transport: transport}))
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
				err := engine.StartExisting(context.Background(), "immutable-id")
				if (err != nil) != tc.wantErr {
					t.Fatalf("error = %v", err)
				}
				if tc.transportErr != nil && !errors.Is(err, tc.transportErr) {
					t.Fatalf("lost transport cause: %v", err)
				}
				if len(paths) == 0 {
					t.Fatal("no start request")
				}
				for _, path := range paths {
					if !strings.HasSuffix(path, "/containers/immutable-id/start") {
						t.Fatalf("unsafe request %v", paths)
					}
				}
			})
		}
	}
}

func TestDockerInspectCancellation(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.43"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := (&DockerEngine{cli: cli}).InspectConfig(ctx, "app"); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not cancel")
	}
}

func TestAdapterLogSinceWire(t *testing.T) {
	p, conn := newContextTestEngine(t, context.Background())
	var podmanSince string
	conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
		podmanSince = r.URL.Query().Get("since")
		return contextResponse(r, 200, ""), nil
	})
	var dockerSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dockerSince = r.URL.Query().Get("since") }))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.43"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	d := &DockerEngine{cli: cli}
	for _, tc := range []struct {
		input    string
		duration time.Duration
		relative bool
	}{
		{"1h", time.Hour, true}, {"0s", 0, true}, {"-1h", -time.Hour, true},
		{"0", 0, false}, {"0.0", 0, false}, {"0.000", 0, false}, {"0.5", 0, false}, {"2024-01-01", 0, false}, {"2024-01-01T03:00:00+03:00", 0, false},
		{"2024-01-01T00:00:00Z", 0, false}, {"1700000000", 0, false}, {"invalid-value", 0, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			before := time.Now()
			for _, adapter := range []struct {
				name string
				call func() error
				got  *string
			}{
				{"docker", func() error { return d.Logs(context.Background(), "app", LogOptions{Since: tc.input}, io.Discard) }, &dockerSince},
				{"podman", func() error { return p.Logs(context.Background(), "app", LogOptions{Since: tc.input}, io.Discard) }, &podmanSince},
			} {
				*adapter.got = "not-sent"
				err := adapter.call()
				if tc.input == "invalid-value" {
					if err == nil || *adapter.got != "not-sent" {
						t.Fatalf("%s: invalid error=%v wire=%q", adapter.name, err, *adapter.got)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", adapter.name, err)
				}
				wire, err := timetypes.GetTimestamp(*adapter.got, before)
				if err != nil {
					t.Fatalf("%s: wire %q: %v", adapter.name, *adapter.got, err)
				}
				seconds, err := strconv.ParseFloat(wire, 64)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.relative {
					want, err := timetypes.GetTimestamp(tc.input, before)
					if err != nil {
						t.Fatal(err)
					}
					wantSeconds, err := strconv.ParseFloat(want, 64)
					if err != nil {
						t.Fatal(err)
					}
					if seconds != wantSeconds {
						t.Fatalf("%s: got %q want %q", adapter.name, wire, want)
					}
					continue
				}
				got := time.Unix(0, int64(seconds*float64(time.Second)))
				if got.Before(before.Add(-tc.duration).Add(-time.Second)) || got.After(time.Now().Add(-tc.duration).Add(time.Second)) {
					t.Fatalf("%s: unexpected timestamp %s", adapter.name, got)
				}
			}
		})
	}
}
