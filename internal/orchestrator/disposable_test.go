package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/umayr/dill/internal/config"
)

func guardrailAdapter(t *testing.T, adapter string, transport contextTransport) Engine {
	t.Helper()
	return guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/images/") {
			return contextResponse(r, 200, `{"Id":"prepared-image","Config":{}}`), nil
		}
		return transport(r)
	}))
}

func guardrailAdapterRaw(t *testing.T, adapter string, transport contextTransport) Engine {
	t.Helper()
	if adapter == "podman" {
		p, conn := newContextTestEngine(t, context.Background())
		conn.Client.Transport = transport
		return p
	}
	cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.43"), client.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return &DockerEngine{cli: cli}
}
func volumeOnlyConfig() *config.DillConfig {
	return &config.DillConfig{Services: map[string]*config.Service{"app": {Image: "synthetic", PullPolicy: "never", Volumes: []json.RawMessage{json.RawMessage(`"data:/data"`)}}}}
}

func TestDisposableUnsupportedMountsAndCapabilities(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, raw := range []string{`"/missing:/data"`, `"./relative:/data"`, `"/data"`, `{"type":"tmpfs","target":"/data"}`, `{"type":"volume","source":"data","name":"shared","target":"/data"}`, `{"type":"volume","source":"data","externalVolume":true,"target":"/data"}`} {
			cfg := volumeOnlyConfig()
			cfg.Engine = engine
			cfg.Services["app"].Volumes = []json.RawMessage{json.RawMessage(raw)}
			if !errors.Is(ValidateDisposableConfig(cfg), ErrDisposableUnsupported) {
				t.Fatalf("%s accepted %s", engine, raw)
			}
		}
		for _, mode := range []string{"host", "container:other", "service:other", "none"} {
			cfg := volumeOnlyConfig()
			cfg.Engine = engine
			cfg.Services["app"].NetworkMode = mode
			if !errors.Is(ValidateDisposableConfig(cfg), ErrDisposableUnsupported) {
				t.Fatalf("%s accepted %s", engine, mode)
			}
		}
	}
}

func TestAdapterDisposableReadOnlyPreflightParity(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, state := range []string{"missing", "owned", "foreign", "nfs-options", "plugin", "inspect-error"} {
			t.Run(adapter+"/"+state, func(t *testing.T) {
				var writes int
				engine := guardrailAdapter(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						writes++
						return contextResponse(r, 500, `{}`), nil
					}
					if strings.Contains(r.URL.Path, "/networks/") {
						return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
					}
					if state == "missing" {
						return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
					}
					if state == "inspect-error" {
						return contextResponse(r, 500, `{"message":"PRIVATE_SENTINEL","response":500}`), nil
					}
					labels, driver, options := `{"dill.managed":"true","dill.stack":"stack"}`, "local", `{}`
					if state == "foreign" {
						labels = `{}`
					}
					if state == "nfs-options" {
						options = `{"device":"/production"}`
					}
					if state == "plugin" {
						driver = "nfs"
					}
					return contextResponse(r, 200, `{"Name":"stack_data","Driver":"`+driver+`","Labels":`+labels+`,"Options":`+options+`}`), nil
				}))
				err := PreflightDisposableResources(context.Background(), engine, volumeOnlyConfig(), "stack")
				wantError := state != "missing" && state != "owned"
				if (err != nil) != wantError || writes != 0 {
					t.Fatalf("error=%v writes=%d", err, writes)
				}
				if err != nil && (strings.Contains(err.Error(), "PRIVATE_SENTINEL") || strings.Contains(err.Error(), "/production")) {
					t.Fatalf("unsafe error: %v", err)
				}
			})
		}
	}
}

func TestAdapterDisposableVolumeMutationRevalidation(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, state := range []string{"owned", "foreign", "options", "create-owned", "create-race"} {
			t.Run(adapter+"/"+state, func(t *testing.T) {
				var creates, deletes int
				engine := guardrailAdapter(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodDelete {
						deletes++
						return contextResponse(r, 204, ""), nil
					}
					if r.Method == http.MethodPost {
						creates++
					}
					if r.Method == http.MethodGet && strings.HasPrefix(state, "create-") {
						return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
					}
					labels, options := `{"dill.managed":"true","dill.stack":"stack"}`, `{}`
					if state == "foreign" || state == "create-race" {
						labels = `{}`
					}
					if state == "options" {
						options = `{"device":"/production"}`
					}
					return contextResponse(r, 200, `{"Name":"stack_data","Driver":"local","Labels":`+labels+`,"Options":`+options+`}`), nil
				}))
				switch e := engine.(type) {
				case *DockerEngine:
					e.disposable = true
				case *PodmanEngine:
					e.disposable = true
				}
				err := engine.EnsureVolume(context.Background(), "stack_data", "stack")
				wantError := state != "owned" && state != "create-owned"
				if (err != nil) != wantError || deletes != 0 {
					t.Fatalf("error=%v creates=%d deletes=%d", err, creates, deletes)
				}
				if !strings.HasPrefix(state, "create-") && creates != 0 {
					t.Fatalf("adopted invalid volume: %d", creates)
				}
			})
		}
	}
}

func TestDisposableWrapperRevalidatesBeforeMutation(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		t.Run(adapter, func(t *testing.T) {
			foreign := false
			var writes int
			engine := guardrailAdapter(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					writes++
					return contextResponse(r, 500, `{}`), nil
				}
				if strings.Contains(r.URL.Path, "/networks/") {
					return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
				}
				labels := `{"dill.managed":"true","dill.stack":"stack"}`
				if foreign {
					labels = `{}`
				}
				return contextResponse(r, 200, `{"Name":"stack_data","Driver":"local","Labels":`+labels+`}`), nil
			}))
			cfg := volumeOnlyConfig()
			guarded, err := WithDisposableGuardrails(context.Background(), engine, cfg, "stack")
			if err != nil {
				t.Fatal(err)
			}
			foreign = true
			if !errors.Is(guarded.EnsureNetwork(context.Background(), "stack"), ErrDisposableOwnership) {
				t.Fatal("network mutation not guarded")
			}
			if _, err := guarded.StartService(context.Background(), "app", cfg.Services["app"], "stack"); !errors.Is(err, ErrDisposableOwnership) {
				t.Fatalf("container mutation not guarded: %v", err)
			}
			if writes != 0 {
				t.Fatalf("mutations after foreign replacement: %d", writes)
			}
		})
	}
}
