package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestInspectedContainerNameEvidenceParity(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, actual := range []string{"/actual-name", "", "/requested"} {
			t.Run(adapter+"/"+actual, func(t *testing.T) {
				id := strings.Repeat("a", 64)
				body, err := json.Marshal(map[string]any{"Id": id, "Name": actual, "Config": map[string]any{}, "HostConfig": map[string]any{}, "NetworkSettings": map[string]any{}})
				if err != nil {
					t.Fatal(err)
				}
				engine := guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						t.Fatal("inspect attempted mutation")
					}
					return contextResponse(r, 200, string(body)), nil
				}))
				live, err := engine.InspectConfig(context.Background(), "requested")
				if err != nil || live == nil {
					t.Fatal(err)
				}
				if live.ID != id || live.InspectedName != strings.TrimPrefix(actual, "/") {
					t.Fatalf("actual identity substituted: %+v", live)
				}
				wire, err := json.Marshal(live)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(wire), "InspectedName") {
					t.Fatal("legacy serialized preview changed")
				}
			})
		}
	}
}

func TestInspectedResourceNamesNeverUseLookupFallback(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, actual := range []string{"actual-name", "", "requested"} {
			t.Run(adapter+"/"+actual, func(t *testing.T) {
				e := secretWireEngine(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						t.Fatal("resource evidence attempted mutation")
					}
					var body map[string]any
					if strings.Contains(r.URL.Path, "/networks/") {
						body = map[string]any{"Name": actual, "Id": strings.Repeat("b", 64), "Driver": "bridge", "name": actual, "id": strings.Repeat("b", 64)}
					} else if strings.Contains(r.URL.Path, "/volumes/") {
						body = map[string]any{"Name": actual, "Driver": "local", "CreatedAt": "2024-01-01T00:00:00Z"}
					} else {
						t.Fatalf("unexpected endpoint %s", r.URL.Path)
					}
					data, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					return contextResponse(r, 200, string(data)), nil
				}))
				facts, err := e.(PreviewResourceInspector).PreviewResources(context.Background(), "requested", []string{"requested"})
				if err != nil || len(facts) != 2 {
					t.Fatalf("facts %v %v", facts, err)
				}
				for _, fact := range facts {
					if !fact.Exists || fact.Name != "requested" || fact.ActualName != actual {
						t.Fatalf("requested name became actual evidence: %+v", fact)
					}
					wire, err := json.Marshal(fact)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(wire), "ActualName") {
						t.Fatal("legacy serialized preview changed")
					}
				}
			})
		}
	}
}
