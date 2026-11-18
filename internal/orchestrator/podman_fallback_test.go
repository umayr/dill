package orchestrator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPodmanAmbientEndpointIsPinned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Libpod-API-Version", "5.0.0") }))
	defer server.Close()
	endpoint := "tcp://" + server.Listener.Addr().String()
	t.Setenv("CONTAINER_HOST", endpoint)
	engine, err := NewPodmanEngine(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if engine.CLIEndpoint() != endpoint {
		t.Fatalf("endpoint = %q", engine.CLIEndpoint())
	}
}

func TestPodmanMissingEndpointFails(t *testing.T) {
	t.Setenv("CONTAINER_HOST", "")
	if _, err := NewPodmanEngine(context.Background(), ""); err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
