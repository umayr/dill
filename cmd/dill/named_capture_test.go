package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

type namedHandlerEngine struct {
	*ownershipEngine
	lists, statuses, inspections int
}

func (e *namedHandlerEngine) ListStack(context.Context, string) ([]string, error) {
	e.lists++
	return nil, errors.New("unexpected target reselection")
}
func (e *namedHandlerEngine) ServiceStatus(context.Context, string) (*orchestrator.ContainerStatus, error) {
	e.statuses++
	return nil, errors.New("unexpected plan recomputation")
}
func (e *namedHandlerEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.inspections++
	return e.ownershipEngine.InspectConfig(ctx, name)
}
func (e *namedHandlerEngine) RemoveNetwork(context.Context, string) error {
	e.mutations = append(e.mutations, "retain-network")
	return errors.New("retained network")
}
func namedHandlerFixture(action string) (*config.DillConfig, *namedHandlerEngine, commandOptions) {
	cfg := testConfig()
	cfg.Services["web"].PullPolicy = "never"
	e := &namedHandlerEngine{ownershipEngine: &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{"app_web": {ID: "reviewed-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}}}}
	capture := &namedCapture{operation: action, stack: "app", compared: true, serviceIDs: map[string]string{"web": "reviewed-id"}, stopIDs: map[string]string{"app_web": "reviewed-id"}, upPlan: &plan.Plan{Changes: []plan.Change{{Service: "web", Kind: plan.KindNoop}}}, removal: &orchestrator.RemovalIntent{Stack: "app", Resources: []orchestrator.OwnedResource{{Name: "app_web", Service: "web", ID: "reviewed-id"}}}}
	state := &operationState{config: cfg, recorder: &operation.Recorder{}, named: capture, ctx: context.Background()}
	return cfg, e, commandOptions{operation: state, Quiet: true, Timeout: time.Second, EngineSocket: "/synthetic-only.sock", EngineSocketSet: true}
}
func TestNamedUpConsumesCapturedPlanAndEmptyOrphanSet(t *testing.T) {
	for _, create := range []bool{false, true} {
		cfg, e, opts := namedHandlerFixture("up")
		if create {
			opts.operation.named.upPlan.Changes[0].Kind = plan.KindCreate
			opts.operation.named.serviceIDs["web"] = ""
			delete(e.live, "app_web")
		}
		err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"network", "start:reviewed-id"}
		if create {
			want = []string{"network", "create"}
		}
		if !reflect.DeepEqual(e.mutations, want) || e.lists != 0 || e.statuses != 0 {
			t.Fatalf("capture reselected: mutations=%v lists=%d statuses=%d", e.mutations, e.lists, e.statuses)
		}
	}
}
func TestNamedUpOrphanAndIncompleteCaptureRejectBeforeEnsure(t *testing.T) {
	for name, mutate := range map[string]func(*namedCapture){
		"orphan": func(c *namedCapture) {
			c.upPlan.Changes = append(c.upPlan.Changes, plan.Change{Service: "old", Kind: plan.KindRemove})
		},
		"uncompared":   func(c *namedCapture) { c.compared = false },
		"wrong action": func(c *namedCapture) { c.operation = "stop" },
		"wrong stack":  func(c *namedCapture) { c.stack = "other" },
		"missing plan": func(c *namedCapture) { c.upPlan = nil },
		"missing IDs":  func(c *namedCapture) { c.serviceIDs = nil },
	} {
		t.Run(name, func(t *testing.T) {
			cfg, e, opts := namedHandlerFixture("up")
			mutate(opts.operation.named)
			opened := false
			err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return e, nil })
			if !errors.Is(err, errNamedCaptureUnavailable) || opened || len(e.mutations) != 0 {
				t.Fatalf("invalid capture reached delegate: %v opened=%v writes=%v", err, opened, e.mutations)
			}
		})
	}
	cfg, e, opts := namedHandlerFixture("up")
	e.live["app_web"].ID = "replacement"
	if err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil }); err == nil || len(e.mutations) != 0 {
		t.Fatal("late capture replacement reached ensure", err, e.mutations)
	}
}
func TestNamedStartRestartStopDownConsumeComparedTargets(t *testing.T) {
	for _, action := range []string{"start", "restart", "stop", "down"} {
		t.Run(action, func(t *testing.T) {
			cfg, e, opts := namedHandlerFixture(action)
			withTestRuntime(t, e, cfg, func() {
				var err error
				switch action {
				case "start":
					err = runStart(context.Background(), opts, nil)
				case "restart":
					err = runRestart(context.Background(), opts, nil)
				case "stop":
					err = runStop(context.Background(), opts, nil)
				case "down":
					err = runDown(context.Background(), opts, nil)
				}
				want := []string{"network", "start:reviewed-id"}
				switch action {
				case "restart":
					want = []string{"stop:reviewed-id", "network", "start:reviewed-id"}
				case "stop":
					want = []string{"stop:reviewed-id"}
				case "down":
					want = []string{"stop:reviewed-id", "remove:reviewed-id", "retain-network"}
				}
				if (err != nil) != (action == "down") || !reflect.DeepEqual(e.mutations, want) || e.lists != 0 || e.statuses != 0 {
					t.Fatalf("targets reselected or retained Down semantics changed: err=%v writes=%v lists=%d status=%d", err, e.mutations, e.lists, e.statuses)
				}
				if action == "stop" && e.inspections != 0 {
					t.Fatal("stop recaptured authoritative targets")
				}
				if action == "down" && e.inspections == 0 {
					t.Fatal("Down removed existing per-call immutable-ID revalidation")
				}
			})
		})
	}
}
func TestNamedCleanupNeverFallsBackToLabelOnlySource(t *testing.T) {
	_, e, opts := namedHandlerFixture("stop")
	opts.operation.config = nil
	if err := runStop(context.Background(), opts, nil); !errors.Is(err, errNamedCaptureUnavailable) || len(e.mutations) != 0 {
		t.Fatal("checked source fell back", err, e.mutations)
	}
}
