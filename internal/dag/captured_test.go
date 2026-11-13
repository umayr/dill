package dag

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type capturedEngine struct {
	orchestrator.Engine
	mu                 sync.Mutex
	live               map[string]*orchestrator.LiveConfig
	writes             []string
	replaceAfterEnsure bool
}

func (e *capturedEngine) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	live := e.live[name]
	if live == nil {
		return nil, orchestrator.ErrNotFound
	}
	copy := *live
	return &copy, nil
}
func (e *capturedEngine) EnsureNetwork(context.Context, string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writes = append(e.writes, "network")
	if e.replaceAfterEnsure {
		for _, live := range e.live {
			live.ID = "replacement"
		}
	}
	return nil
}
func (e *capturedEngine) StartExisting(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writes = append(e.writes, "start:"+id)
	return nil
}
func (e *capturedEngine) StopService(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writes = append(e.writes, "stop:"+id)
	return nil
}
func (e *capturedEngine) RemoveService(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writes = append(e.writes, "remove:"+id)
	return nil
}
func (e *capturedEngine) StartService(_ context.Context, name string, _ *config.Service, _ string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writes = append(e.writes, "create:"+name)
	return "new-" + name, nil
}
func (e *capturedEngine) IsReady(context.Context, string, bool) (bool, error) { return true, nil }
func capturedLive(service, id string) *orchestrator.LiveConfig {
	return &orchestrator.LiveConfig{ID: id, SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": service}}
}
func capturedGraph(t *testing.T, names ...string) *Graph {
	t.Helper()
	services := map[string]*config.Service{}
	for _, name := range names {
		services[name] = &config.Service{Image: "synthetic", PullPolicy: "never"}
	}
	g, err := Build(services)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func runCaptured(g *Graph, e *capturedEngine, ids map[string]string, actions map[string]Action) error {
	return g.RunCaptured(context.Background(), e, "app", time.Second, actions, nil, func(string) PullSink { return discardSink{io.Discard} }, ids)
}
func TestRunCapturedWholeSetBeforeAnyEnsure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ids     map[string]string
		actions map[string]Action
	}{
		{"replacement", map[string]string{"web": "web-id", "db": "old-db"}, map[string]Action{"web": ActionNoop, "db": ActionStart}},
		{"missing entry", map[string]string{"web": "web-id"}, map[string]Action{"web": ActionNoop, "db": ActionStart}},
		{"extra entry", map[string]string{"web": "web-id", "db": "db-id", "extra": "extra-id"}, map[string]Action{"web": ActionNoop, "db": ActionStart}},
		{"nil capture", nil, map[string]Action{"web": ActionNoop, "db": ActionStart}},
		{"claimed absent but present", map[string]string{"web": "", "db": "db-id"}, map[string]Action{"web": ActionCreate, "db": ActionStart}},
		{"missing action", map[string]string{"web": "web-id", "db": "db-id"}, map[string]Action{"web": ActionNoop}},
		{"present creation", map[string]string{"web": "web-id", "db": "db-id"}, map[string]Action{"web": ActionCreate, "db": ActionStart}},
		{"unknown action", map[string]string{"web": "web-id", "db": "db-id"}, map[string]Action{"web": Action(99), "db": ActionStart}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &capturedEngine{live: map[string]*orchestrator.LiveConfig{"app_web": capturedLive("web", "web-id"), "app_db": capturedLive("db", "db-id")}}
			err := runCaptured(capturedGraph(t, "web", "db"), e, tc.ids, tc.actions)
			if !errors.Is(err, ErrCapturedSelectionChanged) {
				t.Fatal("did not reject capture", err)
			}
			if len(e.writes) != 0 {
				t.Fatalf("mutated before exhaustive capture validation: %v", e.writes)
			}
		})
	}
}
func TestRunCapturedExistingAndAbsentUseSameRunner(t *testing.T) {
	for _, action := range []Action{ActionNoop, ActionStart, ActionRecreate, ActionCreate} {
		t.Run(string(rune('0'+action)), func(t *testing.T) {
			e := &capturedEngine{live: map[string]*orchestrator.LiveConfig{}}
			id := "reviewed-id"
			if action == ActionCreate {
				id = ""
			} else {
				e.live["app_web"] = capturedLive("web", id)
			}
			if err := runCaptured(capturedGraph(t, "web"), e, map[string]string{"web": id}, map[string]Action{"web": action}); err != nil {
				t.Fatal(err)
			}
			want := []string{"network", "start:reviewed-id"}
			if action == ActionCreate {
				want = []string{"network", "create:web"}
			}
			if action == ActionRecreate {
				want = []string{"network", "stop:reviewed-id", "remove:reviewed-id", "create:web"}
			}
			if !reflect.DeepEqual(e.writes, want) {
				t.Fatalf("capture was not consumed: %v != %v", e.writes, want)
			}
		})
	}
}
func TestRunCapturedLateReplacementRefusesSubstituteButDoesNotClaimNoEffects(t *testing.T) {
	e := &capturedEngine{live: map[string]*orchestrator.LiveConfig{"app_web": capturedLive("web", "reviewed-id")}, replaceAfterEnsure: true}
	err := runCaptured(capturedGraph(t, "web"), e, map[string]string{"web": "reviewed-id"}, map[string]Action{"web": ActionRecreate})
	if !errors.Is(err, ErrCapturedSelectionChanged) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.writes, []string{"network"}) {
		t.Fatal("late replacement mutated or earlier ensure hidden", e.writes)
	}
}
func TestRunCapturedEmptyStartStillEnsuresNetwork(t *testing.T) {
	e := &capturedEngine{live: map[string]*orchestrator.LiveConfig{}}
	if err := runCaptured(capturedGraph(t), e, map[string]string{}, map[string]Action{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.writes, []string{"network"}) {
		t.Fatal("empty-stack network ensure omitted", e.writes)
	}
}
