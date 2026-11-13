package orchestrator

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestAttachedNetworkInventoryParity(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, tc := range []struct {
			name, body string
			known      bool
			networks   []string
		}{
			{"unknown", `{"Id":"immutable","Config":{},"HostConfig":{"NetworkMode":"bridge"},"NetworkSettings":{}}`, false, nil},
			{"known empty", `{"Id":"immutable","Config":{},"HostConfig":{"NetworkMode":"bridge"},"NetworkSettings":{"Networks":{}}}`, true, nil},
			{"sorted exact identities", `{"Id":"immutable","Config":{},"HostConfig":{"NetworkMode":"bridge"},"NetworkSettings":{"Networks":{"Zoo":{},"alpha":{}}}}`, true, []string{"Zoo", "alpha"}},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				engine := guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, 200, tc.body), nil }))
				live, err := engine.InspectConfig(context.Background(), "immutable")
				if err != nil {
					t.Fatal(err)
				}
				if live.NetworksKnown != tc.known || !reflect.DeepEqual(live.Networks, tc.networks) {
					t.Fatalf("mode is not attachment evidence: known=%v names=%v expected=%v/%v", live.NetworksKnown, live.Networks, tc.known, tc.networks)
				}
			})
		}
	}
}
