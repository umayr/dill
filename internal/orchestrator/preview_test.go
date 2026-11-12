package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPreviewResourceStableWireParity(t *testing.T) {
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			volatile := false
			e := secretWireEngine(t, name, contextTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/networks/") {
					created := "2024-01-01T00:00:00Z"
					if volatile {
						created = "2025-01-01T00:00:00Z"
					}
					if name == "podman" {
						return contextResponse(r, 200, `{"id":"network-id","name":"demo","driver":"bridge","created":"`+created+`","labels":{"dill.stack":"demo"},"options":{}}`), nil
					}
					return contextResponse(r, 200, `{"Id":"network-id","Name":"demo","Driver":"bridge","Created":"`+created+`","Containers":{"irrelevant":{"Name":"dynamic"}},"Labels":{"dill.stack":"demo"},"Options":{}}`), nil
				}
				if strings.Contains(r.URL.Path, "/volumes/") {
					return contextResponse(r, 200, `{"Name":"data","Driver":"local","CreatedAt":"2024-01-01T00:00:00Z","Mountpoint":"/private/volume","Options":{},"Labels":{"dill.stack":"demo"},"MountCount":97,"UsageData":{"Size":123,"RefCount":7}}`), nil
				}
				t.Fatalf("unexpected read endpoint: %s", r.URL.Path)
				return nil, nil
			}))
			inspector := e.(PreviewResourceInspector)
			a, err := inspector.PreviewResources(context.Background(), "demo", []string{"data", "data"})
			if err != nil || len(a) != 2 || a[0].ID != "network-id" || !a[1].Exists {
				t.Fatalf("resource facts unavailable: %v", err)
			}
			volatile = true
			b, err := inspector.PreviewResources(context.Background(), "demo", []string{"data"})
			if err != nil {
				t.Fatal(err)
			}
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			if string(x) != string(y) {
				t.Fatal("irrelevant resource timestamp entered proof")
			}
			text := string(x)
			for _, field := range []string{"MountCount", "UsageData", "RefCount", "Containers", "irrelevant"} {
				if strings.Contains(text, field) {
					t.Fatal("volatile resource fields entered proof")
				}
			}
		})
	}
}
