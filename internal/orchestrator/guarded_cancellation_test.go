package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/secretfile"
)

func TestGuardedOperationsPreserveCancellation(t *testing.T) {
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			e := secretWireEngine(t, name, contextTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _, err := e.(DisposableImageInspector).DisposableImage(ctx, "image")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("image inspect: %v", err)
			}
			_, err = e.(CheckedSecretCreator).StartServiceChecked(ctx, "app", &config.Service{Image: "image"}, "demo", secretfile.Request{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("checked start: %v", err)
			}
		})
	}
}
