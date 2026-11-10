package dag

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type ownershipEngine struct {
	orchestrator.Engine
	live       *orchestrator.LiveConfig
	inspectErr error
	readyErr   error
	startErr   error
	readyCalls int
	started    []string
	stopped    []string
	removed    []string
	created    int
}

func (e *ownershipEngine) EnsureNetwork(context.Context, string) error { return nil }
func (e *ownershipEngine) InspectConfig(context.Context, string) (*orchestrator.LiveConfig, error) {
	return e.live, e.inspectErr
}
func (e *ownershipEngine) StartExisting(_ context.Context, id string) error {
	e.started = append(e.started, id)
	return e.startErr
}
func (e *ownershipEngine) StopService(_ context.Context, id string) error {
	e.stopped = append(e.stopped, id)
	return nil
}
func (e *ownershipEngine) RemoveService(_ context.Context, id string) error {
	e.removed = append(e.removed, id)
	return nil
}
func (e *ownershipEngine) StartService(context.Context, string, *config.Service, string) (string, error) {
	e.created++
	return "new-container-id", nil
}
func (e *ownershipEngine) IsReady(context.Context, string, bool) (bool, error) {
	e.readyCalls++
	return e.readyErr == nil, e.readyErr
}

type discardSink struct{ io.Writer }

func (discardSink) Begin() {}
func (discardSink) Done()  {}

func runOwnershipGraph(t *testing.T, engine *ownershipEngine, action Action) error {
	t.Helper()
	g, err := Build(map[string]*config.Service{"web": {Image: "busybox", ContainerName: "shared-web", PullPolicy: "never"}})
	if err != nil {
		t.Fatal(err)
	}
	return g.Run(context.Background(), engine, "app", time.Second, map[string]Action{"web": action}, nil, func(string) PullSink { return discardSink{io.Discard} })
}

func TestRunRechecksOwnership(t *testing.T) {
	for _, action := range []Action{ActionNoop, ActionStart, ActionRecreate} {
		for _, labels := range []map[string]string{
			nil,
			{"dill.managed": "true", "dill.stack": "other", "dill.service": "web"},
			{"dill.managed": "true", "dill.stack": "app", "dill.service": "db"},
		} {
			engine := &ownershipEngine{live: &orchestrator.LiveConfig{ID: "foreign-id", SystemLabels: labels}}
			err := runOwnershipGraph(t, engine, action)
			if !errors.Is(err, orchestrator.ErrOwnershipConflict) {
				t.Fatalf("action %v: got %v, want ownership conflict", action, err)
			}
			if len(engine.started)+len(engine.stopped)+len(engine.removed)+engine.created != 0 {
				t.Fatalf("foreign container mutated: %+v", engine)
			}
		}
	}
}

func TestRunMutatesOwnedContainerByID(t *testing.T) {
	for _, action := range []Action{ActionNoop, ActionStart, ActionRecreate} {
		engine := &ownershipEngine{live: &orchestrator.LiveConfig{ID: "owned-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}}
		if err := runOwnershipGraph(t, engine, action); err != nil {
			t.Fatal(err)
		}
		if action == ActionRecreate {
			if !reflect.DeepEqual(engine.stopped, []string{"owned-id"}) || !reflect.DeepEqual(engine.removed, []string{"owned-id"}) || engine.created != 1 {
				t.Fatalf("recreate did not target owned ID: %+v", engine)
			}
		} else if !reflect.DeepEqual(engine.started, []string{"owned-id"}) {
			t.Fatalf("start did not target owned ID: %+v", engine)
		}
	}
}

func TestRunInspectionFailureDoesNotMutate(t *testing.T) {
	for _, engine := range []*ownershipEngine{
		{}, // A nil live config must fail ownership checks without mutation or panic.
		{inspectErr: orchestrator.ErrNotFound},
		{inspectErr: errors.New("engine unavailable")},
		{live: &orchestrator.LiveConfig{SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}},
	} {
		for _, action := range []Action{ActionNoop, ActionStart, ActionRecreate} {
			if err := runOwnershipGraph(t, engine, action); err == nil {
				t.Fatalf("action %v: expected inspect/ID error", action)
			}
			if len(engine.started)+len(engine.stopped)+len(engine.removed)+engine.created != 0 {
				t.Fatalf("mutated after failed inspection: %+v", engine)
			}
		}
	}
}

func TestRunStartExistingFailureDoesNotReachReadiness(t *testing.T) {
	for _, action := range []Action{ActionNoop, ActionStart} {
		for _, cause := range []error{errors.New("permission denied"), errors.New("transport failure"), orchestrator.ErrNotFound} {
			engine := &ownershipEngine{startErr: cause, live: &orchestrator.LiveConfig{ID: "owned-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}}
			err := runOwnershipGraph(t, engine, action)
			if !errors.Is(err, cause) || !strings.Contains(err.Error(), "web") || !strings.Contains(err.Error(), "owned-id") {
				t.Fatalf("action %v: lost failure or identity: %v", action, err)
			}
			if engine.readyCalls != 0 || !reflect.DeepEqual(engine.started, []string{"owned-id"}) {
				t.Fatalf("action %v: readiness=%d started=%v", action, engine.readyCalls, engine.started)
			}
		}
	}
}

func TestRollbackUsesCreatedContainerID(t *testing.T) {
	engine := &ownershipEngine{readyErr: errors.New("unhealthy")}
	if err := runOwnershipGraph(t, engine, ActionCreate); err == nil {
		t.Fatal("expected readiness failure")
	}
	if !reflect.DeepEqual(engine.removed, []string{"new-container-id"}) || !reflect.DeepEqual(engine.stopped, []string{"new-container-id"}) {
		t.Fatalf("rollback did not target created ID: %+v", engine)
	}
}
