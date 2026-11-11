package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// The recorder covers the mutation surface of Engine, including operations not
// currently used by up. Reads are deliberately not counted as mutations.
type preMutationEngine struct {
	*recordingUpEngine
	live map[string]*orchestrator.LiveConfig
}

func (e *preMutationEngine) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	if live, ok := e.live[name]; ok {
		return live, nil
	}
	return nil, orchestrator.ErrNotFound
}
func (e *preMutationEngine) ListStack(context.Context, string) ([]string, error) {
	return []string{"test_orphan"}, nil
}
func (e *preMutationEngine) StartExisting(context.Context, string) error {
	e.record("start-existing")
	return nil
}
func (e *preMutationEngine) EnsureVolume(context.Context, string, string) error {
	e.record("ensure-volume")
	return nil
}
func (e *preMutationEngine) RemoveVolume(context.Context, string, string, bool) error {
	e.record("remove-volume")
	return nil
}
func (e *preMutationEngine) RemoveNetwork(context.Context, string) error {
	e.record("remove-network")
	return nil
}
func (e *preMutationEngine) RemoveImage(context.Context, string, bool) error {
	e.record("remove-image")
	return nil
}

func TestConformanceUpFailureBeforeMutation(t *testing.T) {
	negative := -1
	cases := []struct {
		name string
		bad  *config.Service
		want string
	}{
		{"reserved-label", &config.Service{Labels: map[string]string{"dill.stack": "foreign"}}, "reserved prefix"},
		{"invalid-port", &config.Service{Ports: []json.RawMessage{json.RawMessage(`"a:b:c:d"`)}}, "port"},
		{"invalid-mount", &config.Service{Volumes: []json.RawMessage{json.RawMessage(`"/host:/data:rw"`)}}, "volume"},
		{"invalid-resource", &config.Service{MemLimit: "bad"}, "memLimit"},
		{"negative-health-retries", &config.Service{HealthCheck: &config.HealthCheck{Test: json.RawMessage(`"ok"`), Retries: &negative}}, "retries"},
		{"invalid-health-test", &config.Service{HealthCheck: &config.HealthCheck{Test: json.RawMessage(`42`)}}, "health"},
	}
	for _, engine := range []string{"docker", "podman"} {
		for _, tc := range cases {
			for _, selected := range []bool{false, true} {
				t.Run(engine+"/"+tc.name+map[bool]string{false: "/full", true: "/unselected"}[selected], func(t *testing.T) {
					tc.bad.Image = "bad-image"
					cfg := &config.DillConfig{Name: "test", Engine: engine, Services: map[string]*config.Service{"app": {Image: "app", PullPolicy: "always", Volumes: []json.RawMessage{json.RawMessage(`"data:/data"`)}}, "bad": tc.bad}}
					var names []string
					if selected {
						names = []string{"app"}
					}
					e := &preMutationEngine{recordingUpEngine: &recordingUpEngine{fakeEngine: newFakeEngine()}}
					opened := false
					err := runUpConfig(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, cfg, names, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return e, nil })
					if err == nil || !strings.Contains(err.Error(), tc.want) || opened || len(e.events) != 0 {
						t.Fatalf("error=%v opened=%v mutations=%v", err, opened, e.events)
					}
				})
			}
		}
		t.Run(engine+"/unknown-selection", func(t *testing.T) {
			opened := false
			cfg := &config.DillConfig{Name: "test", Engine: engine, Services: map[string]*config.Service{"app": {Image: "app", PullPolicy: "always"}}}
			err := runUpConfig(context.Background(), commandOptions{Quiet: true}, cfg, []string{"missing"}, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return nil, nil })
			if err == nil || !strings.Contains(err.Error(), "unknown service") || opened {
				t.Fatalf("error=%v opened=%v", err, opened)
			}
		})
		t.Run(engine+"/foreign-selected-container", func(t *testing.T) {
			e := &preMutationEngine{recordingUpEngine: &recordingUpEngine{fakeEngine: newFakeEngine()}, live: map[string]*orchestrator.LiveConfig{
				"test_app":    {ID: "foreign"},
				"test_orphan": ownedPreMutationOrphan(),
			}}
			cfg := &config.DillConfig{Name: "test", Engine: engine, Services: map[string]*config.Service{"app": {Image: "app", PullPolicy: "always", Volumes: []json.RawMessage{json.RawMessage(`"data:/data"`)}}}}
			opened := false
			err := runUpConfig(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return e, nil })
			if !opened || !errors.Is(err, orchestrator.ErrOwnershipConflict) || len(e.events) != 0 {
				t.Fatalf("error=%v opened=%v mutations=%v", err, opened, e.events)
			}
		})
		t.Run(engine+"/owned-orphan-control", func(t *testing.T) {
			e := &preMutationEngine{recordingUpEngine: &recordingUpEngine{fakeEngine: newFakeEngine()}, live: map[string]*orchestrator.LiveConfig{"test_orphan": ownedPreMutationOrphan()}}
			cfg := &config.DillConfig{Name: "test", Engine: engine, Services: map[string]*config.Service{"app": {Image: "app", PullPolicy: "never"}}}
			err := runUpConfig(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
			if err != nil {
				t.Fatal(err)
			}
			if len(e.events) < 2 || e.events[0] != "stop:orphan-id" || e.events[1] != "remove:orphan-id" {
				t.Fatalf("owned orphan was not removed: %v", e.events)
			}
		})
	}
}

func ownedPreMutationOrphan() *orchestrator.LiveConfig {
	return &orchestrator.LiveConfig{ID: "orphan-id", SystemLabels: map[string]string{
		"dill.managed": "true", "dill.stack": "test", "dill.service": "orphan",
	}}
}
