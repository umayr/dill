package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAdapterDisposablePreparedImageMetadataParity(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, state := range []string{"prepared", "implicit-volume", "missing-config", "missing-id", "absent", "error"} {
			t.Run(adapter+"/"+state, func(t *testing.T) {
				var writes int
				engine := guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						writes++
						return contextResponse(r, 500, `{}`), nil
					}
					if !strings.Contains(r.URL.Path, "/images/") {
						return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
					}
					body := `{"Id":"prepared-image","Config":{}}`
					switch state {
					case "implicit-volume":
						body = `{"Id":"prepared-image","Config":{"Volumes":{"/implicit-data":{}}}}`
					case "missing-config":
						body = `{"Id":"prepared-image"}`
					case "missing-id":
						body = `{"Config":{}}`
					case "absent":
						return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
					case "error":
						return contextResponse(r, 500, `{"message":"PRIVATE_SENTINEL","response":500}`), nil
					}
					return contextResponse(r, 200, body), nil
				}))
				err := PreflightDisposableResources(context.Background(), engine, volumeOnlyConfig(), "stack")
				if (err != nil) != (state != "prepared") || writes != 0 {
					t.Fatalf("error=%v writes=%d", err, writes)
				}
				if err != nil && strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
					t.Fatalf("raw error escaped: %v", err)
				}
				if state == "implicit-volume" && !errors.Is(err, ErrDisposableUnsupported) {
					t.Fatalf("implicit volume accepted: %v", err)
				}
			})
		}
	}
}

func TestAdapterDisposableCreatePinsPreparedImageID(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		t.Run(adapter, func(t *testing.T) {
			var createdImage string
			var writes int
			engine := guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/images/") {
					return contextResponse(r, 200, `{"Id":"immutable-prepared-image","Config":{}}`), nil
				}
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create") {
					writes++
					var body struct{ Image string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					createdImage = body.Image
					return contextResponse(r, 201, `{"Id":"immutable-created-container-id"}`), nil
				}
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start") {
					writes++
					return contextResponse(r, 204, ""), nil
				}
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}))
			if err := engine.(DisposableVolumeEnforcer).EnforceDisposableVolumes(); err != nil {
				t.Fatal(err)
			}
			cfg := volumeOnlyConfig()
			cfg.Services["app"].Volumes = nil
			if _, err := engine.StartService(context.Background(), "app", cfg.Services["app"], "stack"); err != nil {
				t.Fatal(err)
			}
			if createdImage != "immutable-prepared-image" || writes != 2 {
				t.Fatalf("created image=%q writes=%d", createdImage, writes)
			}
			before := writes
			if !errors.Is(engine.PullImage(context.Background(), "synthetic", io.Discard), ErrDisposableUnsupported) || writes != before {
				t.Fatal("profile allowed pull")
			}
		})
	}
}
