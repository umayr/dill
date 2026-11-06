package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type ownershipEngine struct {
	*fakeEngine
	live      map[string]*orchestrator.LiveConfig
	orphans   []string
	mutations []string
}

func (e *ownershipEngine) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	live, ok := e.live[name]
	if !ok {
		return nil, orchestrator.ErrNotFound
	}
	return live, nil
}
func (e *ownershipEngine) ListStack(context.Context, string) ([]string, error) { return e.orphans, nil }
func (e *ownershipEngine) EnsureNetwork(context.Context, string) error {
	e.mutations = append(e.mutations, "network")
	return nil
}
func (e *ownershipEngine) StopService(_ context.Context, id string) error {
	e.mutations = append(e.mutations, "stop:"+id)
	return nil
}
func (e *ownershipEngine) RemoveService(_ context.Context, id string) error {
	e.mutations = append(e.mutations, "remove:"+id)
	return nil
}
func (e *ownershipEngine) StartExisting(_ context.Context, id string) error {
	e.mutations = append(e.mutations, "start:"+id)
	return nil
}
func (e *ownershipEngine) StartService(context.Context, string, *config.Service, string) (string, error) {
	e.mutations = append(e.mutations, "create")
	return "new-id", nil
}

func TestCommandsRejectForeignContainers(t *testing.T) {
	for _, name := range []string{"app_web", "shared-web"} {
		for _, labels := range []map[string]string{
			nil,
			{"dill.managed": "false", "dill.stack": "app", "dill.service": "web"},
			{"dill.managed": "true", "dill.stack": "other", "dill.service": "web"},
			{"dill.managed": "true", "dill.stack": "app", "dill.service": "other"},
		} {
			for _, args := range [][]string{{"plan"}, {"diff"}, {"up"}, {"up", "--force-recreate"}, {"up", "--dry-run"}} {
				engine := &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{name: {ID: "foreign-id", Image: "busybox", SystemLabels: labels}}}
				cfg := testConfig()
				if name == "shared-web" {
					cfg.Services["web"].ContainerName = name
				}
				cfg.Services["web"].PullPolicy = "always"
				withTestRuntime(t, engine, cfg, func() {
					_, _, err := captureCommand(t, "", func() error { return run(args) })
					if !errors.Is(err, orchestrator.ErrOwnershipConflict) {
						t.Fatalf("%v with labels %v: got %v, want ownership conflict", args, labels, err)
					}
				})
				if len(engine.mutations) != 0 || len(engine.pulled) != 0 {
					t.Fatalf("mutated foreign collision: mutations %v, pulls %v", engine.mutations, engine.pulled)
				}
			}
		}
	}
}

func TestUpOwnedContainerForceRecreate(t *testing.T) {
	for _, force := range []bool{false, true} {
		engine := &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{"app_web": {ID: "owned-id", Image: "busybox", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}}}
		cfg := testConfig()
		cfg.Services["web"].PullPolicy = "never"
		withTestRuntime(t, engine, cfg, func() {
			if err := runUp(context.Background(), commandOptions{Quiet: true, ForceRecreate: force, Timeout: time.Second}, nil); err != nil {
				t.Fatal(err)
			}
		})
		want := []string{"network", "start:owned-id"}
		if force {
			want = []string{"network", "stop:owned-id", "remove:owned-id", "create"}
		}
		if !reflect.DeepEqual(engine.mutations, want) {
			t.Fatalf("force=%v: mutations %v, want %v", force, engine.mutations, want)
		}
	}
}

func TestUpRechecksOrphanOwnership(t *testing.T) {
	engine := &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{"app_old": {ID: "foreign-id"}}, orphans: []string{"app_old"}}
	withTestRuntime(t, engine, testConfig(), func() {
		err := runUp(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, nil)
		if !errors.Is(err, orchestrator.ErrOwnershipConflict) {
			t.Fatalf("got %v, want ownership conflict", err)
		}
	})
	if len(engine.mutations) != 0 {
		t.Fatalf("mutated stale orphan: %v", engine.mutations)
	}
}
