package main

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

type noopPullSink struct{}

func (noopPullSink) Write(p []byte) (int, error) { return len(p), nil }
func (noopPullSink) Begin()                      {}
func (noopPullSink) Done()                       {}

func TestUpCapabilityRejectionBeforeEngineOpen(t *testing.T) {
	cases := []struct {
		name    string
		service *config.Service
		want    string
	}{
		{"device", &config.Service{Image: "image", PullPolicy: "always", Devices: []string{"/dev/a"}}, "device"},
		{"network", &config.Service{Image: "image", PullPolicy: "always", NetworkMode: "container:other"}, "network mode"},
		{"port", &config.Service{Image: "image", PullPolicy: "always", Ports: []json.RawMessage{json.RawMessage(`"80"`)}}, "unpublished port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := false
			cfg := &config.DillConfig{Engine: "podman", Name: "stack", Services: map[string]*config.Service{"app": tc.service}}
			err := runUpConfig(context.Background(), commandOptions{}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) {
				opened = true
				return newFakeEngine(), nil
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) || opened {
				t.Fatalf("error=%v opened=%v", err, opened)
			}
		})
	}
}

func TestPullAlwaysImages(t *testing.T) {
	fake := newFakeEngine()
	services := map[string]*config.Service{
		"always":  {Image: "always:1", PullPolicy: "always"},
		"missing": {Image: "missing:1", PullPolicy: "missing"},
		"never":   {Image: "never:1", PullPolicy: "never"},
	}

	err := pullAlwaysImages(context.Background(), fake, services, func(string) dag.PullSink {
		return noopPullSink{}
	})
	if err != nil {
		t.Fatalf("pullAlwaysImages: %v", err)
	}
	if len(fake.pulled) != 1 || fake.pulled[0] != "always:1" {
		t.Fatalf("pulled = %v, want [always:1]", fake.pulled)
	}
	if services["always"].PullPolicy != "missing" {
		t.Errorf("always policy = %q, want missing after pre-pull", services["always"].PullPolicy)
	}
	if services["missing"].PullPolicy != "missing" || services["never"].PullPolicy != "never" {
		t.Errorf("policies changed unexpectedly: %+v", services)
	}
}

func TestFilterPlanSubset(t *testing.T) {
	p := &plan.Plan{Changes: []plan.Change{
		{Service: "app", Kind: plan.KindCreate},
		{Service: "dep", Kind: plan.KindNoop},
		{Service: "other", Kind: plan.KindCreate},
		{Service: "orphan-container", Kind: plan.KindRemove},
	}}
	selected := map[string]*config.Service{"app": {Image: "x"}}

	got := filterPlan(p, selected, false)
	if len(got.Changes) != 1 || got.Changes[0].Service != "app" {
		t.Fatalf("filterPlan = %+v, want only app", got.Changes)
	}
}

func TestFilterPlanFullKeepsOrphans(t *testing.T) {
	changes := []plan.Change{
		{Service: "app", Kind: plan.KindCreate},
		{Service: "other", Kind: plan.KindNoop},
		{Service: "orphan-container", Kind: plan.KindRemove},
	}
	selected := map[string]*config.Service{
		"app":   {Image: "x"},
		"other": {Image: "x"},
	}

	got := filterPlan(&plan.Plan{Changes: changes}, selected, true)
	if len(got.Changes) != 3 {
		t.Fatalf("filterPlan = %+v, want all 3 changes", got.Changes)
	}
}

func TestWithoutDependencies(t *testing.T) {
	services := map[string]*config.Service{
		"app": {Image: "x", DependsOn: []string{"dep"}},
	}
	got := withoutDependencies(services)
	if len(got["app"].DependsOn) != 0 {
		t.Fatalf("copy still has dependencies: %+v", got["app"].DependsOn)
	}
	if len(services["app"].DependsOn) != 1 {
		t.Fatalf("original was mutated: %+v", services["app"].DependsOn)
	}
}

type recordingUpEngine struct {
	*fakeEngine
	mu     sync.Mutex
	events []string
}

func (f *recordingUpEngine) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, s)
}
func (f *recordingUpEngine) PullImage(_ context.Context, image string, _ io.Writer) error {
	f.record("pull:" + image)
	return nil
}
func (f *recordingUpEngine) ImageExists(context.Context, string) (bool, error) { return true, nil }
func (f *recordingUpEngine) EnsureNetwork(context.Context, string) error {
	f.record("network")
	return nil
}
func (f *recordingUpEngine) StopService(_ context.Context, name string) error {
	f.record("stop:" + name)
	return nil
}
func (f *recordingUpEngine) RemoveService(_ context.Context, name string) error {
	f.record("remove:" + name)
	return nil
}
func (f *recordingUpEngine) StartService(_ context.Context, name string, _ *config.Service, _ string) (string, error) {
	f.record("start:" + name)
	return "id", nil
}
func (f *recordingUpEngine) InspectConfig(context.Context, string) (*orchestrator.LiveConfig, error) {
	f.record("inspect")
	return nil, orchestrator.ErrNotFound
}

func TestRunUpConfigPreflight(t *testing.T) {
	badDuration := json.RawMessage(`{"value":1,"unit":"bogus"}`)
	tests := []struct {
		name                 string
		services             map[string]*config.Service
		names                []string
		noDeps, dry, wantErr bool
		wantOpen             bool
	}{
		{"cycle", map[string]*config.Service{"a": {Image: "a", PullPolicy: "always", DependsOn: []string{"b"}}, "b": {Image: "b", DependsOn: []string{"a"}}}, nil, false, false, true, false},
		{"missing", map[string]*config.Service{"a": {Image: "a", PullPolicy: "always", DependsOn: []string{"missing"}}}, nil, false, false, true, false},
		{"cycle dry run", map[string]*config.Service{"a": {Image: "a", DependsOn: []string{"a"}}}, nil, false, true, true, false},
		{"bad duration", map[string]*config.Service{"a": {Image: "a", PullPolicy: "always", HealthCheck: &config.HealthCheck{Test: json.RawMessage(`"ok"`), Interval: &badDuration}}}, nil, false, false, true, false},
		{"unselected cycle", map[string]*config.Service{"a": {Image: "a"}, "b": {Image: "b", DependsOn: []string{"b"}}}, []string{"a"}, false, true, true, false},
		{"no deps cycle", map[string]*config.Service{"a": {Image: "a", DependsOn: []string{"a"}}}, []string{"a"}, true, true, true, false},
		{"selected dependency cycle", map[string]*config.Service{"a": {Image: "a", DependsOn: []string{"b"}}, "b": {Image: "b", DependsOn: []string{"a"}}}, []string{"a"}, false, true, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &recordingUpEngine{fakeEngine: newFakeEngine()}
			opened := false
			err := runUpConfig(context.Background(), commandOptions{DryRun: tc.dry, Quiet: true, Timeout: time.Second}, &config.DillConfig{Name: "test", Services: tc.services}, tc.names, tc.noDeps, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return f, nil })
			if (err != nil) != tc.wantErr || opened != tc.wantOpen {
				t.Fatalf("error=%v opened=%v", err, opened)
			}
			for _, event := range f.events {
				if event != "inspect" {
					t.Fatalf("unexpected mutation: %v", f.events)
				}
			}
		})
	}
}

func TestRunUpConfigOwnershipBeforePullAndCreate(t *testing.T) {
	f := &recordingUpEngine{fakeEngine: newFakeEngine()}
	cfg := &config.DillConfig{Name: "test", Services: map[string]*config.Service{"a": {Image: "a", PullPolicy: "always"}}}
	err := runUpConfig(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return f, nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"inspect", "pull:a", "inspect", "network", "start:a"}
	if !reflect.DeepEqual(f.events, want) {
		t.Fatalf("events = %v, want %v", f.events, want)
	}
}
