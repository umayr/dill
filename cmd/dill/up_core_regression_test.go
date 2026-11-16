package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type deployRegressionEngine struct {
	*recordingUpEngine
	orphans             []string
	badOrphan, failStop bool
	cancel              context.CancelFunc
}

func newDeployRegressionEngine() *deployRegressionEngine {
	return &deployRegressionEngine{recordingUpEngine: &recordingUpEngine{fakeEngine: newFakeEngine()}}
}
func (e *deployRegressionEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.record("inspect:" + name)
	for _, n := range e.orphans {
		if n == name {
			service := strings.TrimPrefix(n, "test_")
			stack := "test"
			if e.badOrphan && service == "a" {
				stack = "foreign"
			}
			return &orchestrator.LiveConfig{ID: service + "-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": stack, "dill.service": service}}, nil
		}
	}
	return nil, orchestrator.ErrNotFound
}
func (e *deployRegressionEngine) ListStack(context.Context, string) ([]string, error) {
	e.record("list")
	return e.orphans, nil
}
func (e *deployRegressionEngine) PullImage(ctx context.Context, image string, _ io.Writer) error {
	e.record("pull:" + image)
	if e.cancel != nil {
		e.cancel()
		return ctx.Err()
	}
	return nil
}
func (e *deployRegressionEngine) StopService(_ context.Context, id string) error {
	e.record("stop:" + id)
	if e.failStop && id == "a-id" {
		return errors.New("stop failed")
	}
	return nil
}
func (e *deployRegressionEngine) RemoveService(_ context.Context, id string) error {
	e.record("remove:" + id)
	return nil
}

func regressionRun(ctx context.Context, direct bool, cfg *config.DillConfig, e *deployRegressionEngine, names []string, noDeps, dry bool, pull string) error {
	if direct {
		return core.Deploy(ctx, cfg, core.DeployOptions{Stack: "test", Services: names, NoDeps: noDeps, DryRun: dry, PullPolicy: pull, Timeout: time.Second, Open: func(context.Context, *core.Config) (core.DeployEngine, error) { return e, nil }})
	}
	return runUpConfig(ctx, commandOptions{Quiet: true, DryRun: dry, Timeout: time.Second}, cfg, names, noDeps, pull, func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
}
func containsEvent(events []string, event string) bool {
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}
func eventIndex(events []string, event string) int {
	for i, e := range events {
		if e == event {
			return i
		}
	}
	return -1
}

func TestOrdinaryDeployRegressionParity(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, direct := range []bool{false, true} {
			mode := "cli"
			if direct {
				mode = "core"
			}
			prefix := engine + "/" + mode
			cfg := func() *config.DillConfig {
				return &config.DillConfig{Name: "test", Engine: engine, Services: map[string]*config.Service{"web": {Image: "web", PullPolicy: "always"}}}
			}
			t.Run(prefix+"/orphan-preflight", func(t *testing.T) {
				e := newDeployRegressionEngine()
				e.orphans = []string{"test_a", "test_b"}
				e.badOrphan = true
				err := regressionRun(context.Background(), direct, cfg(), e, nil, false, false, "")
				if !errors.Is(err, orchestrator.ErrOwnershipConflict) {
					t.Fatalf("err=%v events=%v", err, e.events)
				}
				for _, x := range e.events {
					if strings.HasPrefix(x, "pull:") || strings.HasPrefix(x, "stop:") || strings.HasPrefix(x, "remove:") || x == "network" {
						t.Fatalf("premature mutation %v", e.events)
					}
				}
			})
			t.Run(prefix+"/orphan-partial-failure", func(t *testing.T) {
				e := newDeployRegressionEngine()
				e.orphans = []string{"test_a", "test_b"}
				e.failStop = true
				c := cfg()
				c.Services["web"].PullPolicy = "never"
				err := regressionRun(context.Background(), direct, c, e, nil, false, false, "")
				if err == nil {
					t.Fatal("expected failure")
				}
				for _, x := range []string{"stop:a-id", "remove:a-id", "stop:b-id", "remove:b-id"} {
					if !containsEvent(e.events, x) {
						t.Fatalf("missing %s: %v", x, e.events)
					}
				}
				if containsEvent(e.events, "network") || containsEvent(e.events, "start:web") {
					t.Fatalf("deployment proceeded: %v", e.events)
				}
			})
			t.Run(prefix+"/always-replan", func(t *testing.T) {
				e := newDeployRegressionEngine()
				c := cfg()
				err := regressionRun(context.Background(), direct, c, e, nil, false, false, "")
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, x := range e.events {
					if x == "inspect:test_web" {
						count++
					}
				}
				p := eventIndex(e.events, "pull:web")
				if count != 2 || p < 1 || p >= eventIndex(e.events, "network") || c.Services["web"].PullPolicy != "always" {
					t.Fatalf("events=%v config=%+v", e.events, c.Services["web"])
				}
			})
			t.Run(prefix+"/no-deps-override", func(t *testing.T) {
				e := newDeployRegressionEngine()
				e.orphans = []string{"test_a"}
				c := cfg()
				c.Services["web"].DependsOn = []string{"db"}
				c.Services["db"] = &config.Service{Image: "db", PullPolicy: "always"}
				before := append([]string(nil), c.Services["web"].DependsOn...)
				err := regressionRun(context.Background(), direct, c, e, []string{"web"}, true, false, "never")
				if err != nil {
					t.Fatal(err)
				}
				if !containsEvent(e.events, "start:web") || containsEvent(e.events, "start:db") || containsEvent(e.events, "pull:web") || containsEvent(e.events, "stop:a-id") || !reflect.DeepEqual(before, c.Services["web"].DependsOn) || c.Services["web"].PullPolicy != "always" {
					t.Fatalf("events=%v config=%+v", e.events, c.Services["web"])
				}
			})
			t.Run(prefix+"/dry-run", func(t *testing.T) {
				e := newDeployRegressionEngine()
				err := regressionRun(context.Background(), direct, cfg(), e, nil, false, true, "")
				if err != nil {
					t.Fatal(err)
				}
				for _, x := range e.events {
					if strings.HasPrefix(x, "pull:") || strings.HasPrefix(x, "stop:") || strings.HasPrefix(x, "remove:") || strings.HasPrefix(x, "start:") || x == "network" {
						t.Fatalf("dry-run mutation: %v", e.events)
					}
				}
			})
			t.Run(prefix+"/dependency-order", func(t *testing.T) {
				e := newDeployRegressionEngine()
				c := cfg()
				c.Services["web"].PullPolicy = "never"
				c.Services["web"].DependsOn = []string{"db"}
				c.Services["db"] = &config.Service{Image: "db", PullPolicy: "never"}
				err := regressionRun(context.Background(), direct, c, e, []string{"web"}, false, false, "")
				if err != nil {
					t.Fatal(err)
				}
				db, web := eventIndex(e.events, "start:db"), eventIndex(e.events, "start:web")
				if db < 0 || web <= db {
					t.Fatalf("order %v", e.events)
				}
			})
			t.Run(prefix+"/cancel-during-pull", func(t *testing.T) {
				e := newDeployRegressionEngine()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				e.cancel = cancel
				err := regressionRun(ctx, direct, cfg(), e, nil, false, false, "")
				if !errors.Is(err, context.Canceled) || containsEvent(e.events, "network") || containsEvent(e.events, "start:web") {
					t.Fatalf("err=%v events=%v", err, e.events)
				}
			})
		}
	}
}
