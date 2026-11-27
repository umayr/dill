package orchestrator

import (
	"context"
	"github.com/containers/podman/v5/pkg/bindings"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQualifiedPodmanSDKContextUsesOnlyBoundClientAfterPing(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "dill-sdk-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "engine.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap, mutations atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bootstrap.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			t.Error("bootstrap connection used beyond initialization")
		}
		w.Header().Set("Libpod-API-Version", "9.9.9")
		w.WriteHeader(200)
	})}
	go server.Serve(l)
	defer server.Close()
	bound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mutations.Add(1); w.WriteHeader(200) }))
	defer bound.Close()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		copy.URL = &u
		copy.URL.Scheme = "http"
		copy.URL.Host = strings.TrimPrefix(bound.URL, "http://")
		copy.Host = copy.URL.Host
		return http.DefaultTransport.RoundTrip(copy)
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, err := boundPodmanContext(ctx, "unix://"+path, client, "5.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if bindings.ServiceVersion(connection).String() != "5.0.0" {
		t.Fatal("bootstrap version supplied authority")
	}
	sdk, err := bindings.GetClient(connection)
	if err != nil || sdk.Client != client {
		t.Fatal("SDK did not retain exact qualified client")
	}
	response, err := sdk.DoRequest(ctx, nil, http.MethodPost, "/containers/create", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bootstrap.Load() != 1 || mutations.Load() != 1 {
		t.Fatal("SDK escaped to bootstrap transport")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
