package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/orchestrator"
)

type reuseDuringConfirmationEngine struct {
	*fakeEngine
	inspects  int
	mutations []string
}

func (e *reuseDuringConfirmationEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	live, err := e.fakeEngine.InspectConfig(ctx, name)
	if err != nil {
		return live, err
	}
	e.inspects++
	if e.inspects > 1 {
		live.ID = "same-stack-replacement"
	}
	return live, nil
}
func (e *reuseDuringConfirmationEngine) RemoveService(_ context.Context, id string) error {
	e.mutations = append(e.mutations, "remove:"+id)
	return nil
}
func (e *reuseDuringConfirmationEngine) StopService(_ context.Context, id string) error {
	e.mutations = append(e.mutations, "stop:"+id)
	return nil
}

func TestRmConfirmationCannotAuthorizeNameReuse(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			e := &reuseDuringConfirmationEngine{fakeEngine: newFakeEngine()}
			e.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
			cfg := testConfig()
			cfg.Engine = engine
			withTestRuntime(t, e, cfg, func() {
				_, stderr, err := captureCommand(t, "y\n", func() error { return runRm(context.Background(), commandOptions{ConfigFile: "compose.pkl"}, nil) })
				if !errors.Is(err, orchestrator.ErrOwnershipConflict) || !strings.Contains(stderr, "app_web (id app_web)") || !strings.Contains(stderr, "[y/N]") || len(e.mutations) != 0 {
					t.Fatalf("error=%v prompt=%q mutations=%v", err, stderr, e.mutations)
				}
			})
		})
	}
}

type imageReusedEngine struct {
	*failingLifecycleEngine
	imageInspects int
}

func (e *imageReusedEngine) LocalImageID(context.Context, string) (string, error) {
	e.imageInspects++
	if e.imageInspects == 1 {
		return "captured-image", nil
	}
	return "new-image", nil
}
func TestTeardownRetainsRetaggedImageAfterConfirmation(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			e := &imageReusedEngine{failingLifecycleEngine: &failingLifecycleEngine{fakeEngine: newFakeEngine()}}
			cfg := testConfig()
			cfg.Engine = engine
			withTestRuntime(t, e, cfg, func() {
				_, stderr, err := captureCommand(t, "y\n", func() error { return runTeardown(context.Background(), commandOptions{ConfigFile: "compose.pkl"}, nil) })
				if !errors.Is(err, orchestrator.ErrOwnershipConflict) || !strings.Contains(stderr, "captured-image") {
					t.Fatalf("error=%v prompt=%q", err, stderr)
				}
				for _, call := range e.calls {
					if strings.HasPrefix(call, "image ") {
						t.Fatalf("removed unconfirmed image: %v", e.calls)
					}
				}
			})
		})
	}
}
