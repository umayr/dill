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

func TestPodmanLogSinceRequest(t *testing.T) {
	p, conn := newContextTestEngine(t, context.Background())
	conn.Client.Transport = contextTransport(func(r *http.Request) (*http.Response, error) {
		s := r.URL.Query().Get("since")
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			t.Errorf("since=%q: %v", s, err)
		}
		return contextResponse(r, 200, ""), nil
	})
	if err := p.Logs(context.Background(), "app", LogOptions{Since: "1h"}, io.Discard); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
}
