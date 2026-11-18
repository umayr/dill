package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPodmanDiscoveryFailurePinsFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PODMAN_SOCKET", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Libpod-API-Version", "5.0.0") }))
	defer server.Close()
	endpoint := "tcp://" + server.Listener.Addr().String()
	t.Setenv("CONTAINER_HOST", endpoint)
	engine, err := newPodmanEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, args := engineCommand(engine, "ps")
	if len(args) < 2 || args[0] != "--url" || args[1] != endpoint {
		t.Fatalf("endpoint arguments = %q", args)
	}
}

func TestPodmanDiscoveryCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PODMAN_SOCKET", "")
	t.Setenv("CONTAINER_HOST", "tcp://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := newPodmanEngine(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("error = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("discovery did not stop promptly")
	}
}
