package orchestrator

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAdapterRemovalIntentPinsOwnershipAndIDs(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, scenario := range []string{"owned", "same-stack-name-reuse", "foreign-name-reuse", "stop-failure", "reuse-after-stop", "cancelled"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				id, labels := "captured-id", `{"dill.managed":"true","dill.stack":"stack","dill.service":"app"}`
				var mutations []string
				engine := guardrailAdapter(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						return contextResponse(r, 200, `{"Id":"`+id+`","Config":{"Labels":`+labels+`},"HostConfig":{},"NetworkSettings":{}}`), nil
					}
					mutations = append(mutations, r.Method+" "+r.URL.Path)
					if strings.HasSuffix(r.URL.Path, "/stop") {
						if scenario == "reuse-after-stop" {
							id = "replacement-id"
						}
						if scenario == "stop-failure" {
							return contextResponse(r, 500, `{"message":"stop failed","response":500}`), nil
						}
					}
					if adapter == "podman" && r.Method == http.MethodDelete {
						return contextResponse(r, 200, `[]`), nil
					}
					return contextResponse(r, 204, ""), nil
				}))
				intent, err := CaptureRemovalIntent(context.Background(), engine, "stack", []RemovalTarget{{Name: "stack_app", Service: "app"}})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch scenario {
				case "same-stack-name-reuse":
					id = "replacement-id"
				case "foreign-name-reuse":
					id = "foreign-id"
					labels = `{}`
				case "cancelled":
					cancel()
				}
				err = intent.Remove(ctx, engine, intent.Resources[0])
				switch scenario {
				case "same-stack-name-reuse", "foreign-name-reuse":
					if !errors.Is(err, ErrOwnershipConflict) || len(mutations) != 0 {
						t.Fatalf("error=%v writes=%v", err, mutations)
					}
				case "cancelled":
					if !errors.Is(err, context.Canceled) || len(mutations) != 0 {
						t.Fatalf("error=%v writes=%v", err, mutations)
					}
				case "reuse-after-stop":
					if !errors.Is(err, ErrOwnershipConflict) || len(mutations) != 1 {
						t.Fatalf("error=%v writes=%v", err, mutations)
					}
				default:
					if (err != nil) != (scenario == "stop-failure") || len(mutations) != 2 {
						t.Fatalf("error=%v writes=%v", err, mutations)
					}
				}
				for _, mutation := range mutations {
					if !strings.Contains(mutation, "/containers/captured-id") {
						t.Fatalf("name targeted: %s", mutation)
					}
				}
			})
		}
	}
}

// Models an operation observer/decorator without importing a dependent lane.
type guardrailDecorator struct{ Engine }

func (e *guardrailDecorator) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	return e.Engine.(PreviewResourceInspector).PreviewResources(ctx, stack, names)
}
func (e *guardrailDecorator) EnforceDisposableVolumes() error {
	enforcer, ok := e.Engine.(DisposableVolumeEnforcer)
	if !ok {
		return ErrDisposableUnavailable
	}
	return enforcer.EnforceDisposableVolumes()
}
func (e *guardrailDecorator) EngineName() string {
	return e.Engine.(interface{ EngineName() string }).EngineName()
}

type readOnlyGuardrailDecorator struct{ Engine }

func (e *readOnlyGuardrailDecorator) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	return e.Engine.(PreviewResourceInspector).PreviewResources(ctx, stack, names)
}

func TestDisposableDecoratorRequiresExplicitEnforcement(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		t.Run(adapter, func(t *testing.T) {
			foreign := false
			var writes int
			base := guardrailAdapter(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
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
			if _, err := WithDisposableGuardrails(context.Background(), &readOnlyGuardrailDecorator{base}, cfg, "stack"); !errors.Is(err, ErrDisposableUnavailable) {
				t.Fatalf("unsupported decorator: %v", err)
			}
			decorated := &guardrailDecorator{base}
			guarded, err := WithDisposableGuardrails(context.Background(), decorated, cfg, "stack")
			if err != nil {
				t.Fatal(err)
			}
			if guarded.(interface{ EngineName() string }).EngineName() != adapter {
				t.Fatal("engine identity lost")
			}
			foreign = true
			// Exercise the delegated adapter at volume creation time, not just the
			// wrapper's earlier read-only preflight. No bypass via decoration.
			if err := decorated.EnsureVolume(context.Background(), "stack_data", "stack"); !errors.Is(err, ErrDisposableOwnership) {
				t.Fatalf("adapter enforcement lost: %v", err)
			}
			if writes != 0 {
				t.Fatalf("writes=%d", writes)
			}
		})
	}
}
