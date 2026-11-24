package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/secretfile"
)

type secretSpy struct {
	*fakeEngine
	mu     sync.Mutex
	events []string
	live   map[string]*orchestrator.LiveConfig
}

func newSecretSpy() *secretSpy {
	return &secretSpy{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{}}
}
func (s *secretSpy) event(e string) { s.mu.Lock(); defer s.mu.Unlock(); s.events = append(s.events, e) }
func (s *secretSpy) PullImage(_ context.Context, image string, w io.Writer) error {
	s.event("pull " + image)
	fmt.Fprintln(w, "synthetic-private-sdk-progress")
	return nil
}
func (s *secretSpy) EnsureNetwork(context.Context, string) error { s.event("network"); return nil }
func (s *secretSpy) StartService(_ context.Context, name string, _ *config.Service, _ string) (string, error) {
	s.event("create " + name)
	return "created-" + name, nil
}
func (s *secretSpy) StartExisting(_ context.Context, id string) error {
	s.event("start " + id)
	return nil
}
func (s *secretSpy) StopService(_ context.Context, id string) error {
	s.event("stop " + id)
	return nil
}
func (s *secretSpy) RemoveService(_ context.Context, id string) error {
	s.event("remove " + id)
	return nil
}
func (s *secretSpy) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	s.event("inspect " + name)
	if l := s.live[name]; l != nil {
		return l, nil
	}
	return nil, orchestrator.ErrNotFound
}
func (s *secretSpy) ListStack(context.Context, string) ([]string, error) {
	names := []string{}
	for name := range s.live {
		names = append(names, name)
	}
	return names, nil
}
func (s *secretSpy) owned(name, service string) {
	s.live[name] = &orchestrator.LiveConfig{ID: "immutable-" + name, Image: "image", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "demo", "dill.service": service}}
}
func (s *secretSpy) mutations() []string {
	var out []string
	for _, e := range s.events {
		for _, p := range []string{"pull ", "create ", "network", "start ", "stop ", "remove "} {
			if strings.HasPrefix(e, p) {
				out = append(out, e)
			}
		}
	}
	return out
}
func secretConfig() *config.DillConfig {
	return &config.DillConfig{Name: "demo", Engine: "docker", Secrets: []config.SecretRequirement{{Name: "db"}}, Services: map[string]*config.Service{
		"app":   {Image: "image", DependsOn: []string{"dep"}, PullPolicy: "always"},
		"dep":   {Image: "image", PullPolicy: "missing", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", Mode: 256}}},
		"other": {Image: "image", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", Mode: 256}}},
	}}
}
func secretOptions() commandOptions {
	return commandOptions{ConfigFile: "synthetic.pkl", Engine: "docker", EngineSocket: "/run/synthetic.sock", EngineSocketSet: true, StackName: "demo", SecretPolicy: "/synthetic/policy", SecretPolicyCaptured: true, SecretTarget: "lab", SecretBindings: "/synthetic/bindings", SecretBindingsSet: true, Timeout: time.Second, Quiet: true}
}

func TestSecretDirectStartRestartDenyBeforeAnyMutation(t *testing.T) {
	for _, command := range []string{"start", "restart"} {
		for _, marker := range []string{"true", "false", "malformed"} {
			t.Run(command+"/"+marker, func(t *testing.T) {
				s := newSecretSpy()
				s.owned("demo_app", "app")
				s.live["demo_app"].SystemLabels[orchestrator.SecretEnabledLabel] = marker
				c := &config.DillConfig{Name: "demo", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "image"}}}
				withTestRuntime(t, s, c, func() {
					_, _, err := captureCommand(t, "", func() error {
						return run([]string{"-p", "demo", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", command})
					})
					if err != config.ErrSecretUnavailable || len(s.mutations()) != 0 {
						t.Fatalf("marked direct lifecycle mutated: %v %v", err, s.events)
					}
				})
			})
		}
	}
}
func TestSecretCleanupDoesNotRequireAvailabilityOrDeleteSources(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "synthetic-source")
	if err := os.WriteFile(source, []byte("synthetic"), 0400); err != nil {
		t.Fatal(err)
	}
	s := newSecretSpy()
	s.owned("demo_app", "app")
	s.live["demo_app"].SystemLabels[orchestrator.SecretEnabledLabel] = "true"
	oldPre := secretPreflight
	secretPreflight = func(context.Context, secretfile.Request) (*secretfile.Result, error) {
		t.Fatal("cleanup called availability preflight")
		return nil, config.ErrSecretUnavailable
	}
	defer func() { secretPreflight = oldPre }()
	oldLoader, oldEngine := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return nil, errors.New("missing synthetic recipe")
	}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return s, nil }
	_, _, err := captureCommand(t, "", func() error {
		return run([]string{"-p", "demo", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "down"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("external synthetic source deleted")
	}
	if !strings.Contains(strings.Join(s.events, ","), "remove immutable-demo_app") {
		t.Fatalf("cleanup not ownership/ID bound: %v", s.events)
	}
}

func TestDirectLifecycleMarkerSurvivesContainerNameChange(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprint(restart), func(t *testing.T) {
			s := newSecretSpy()
			s.owned("old-custom-name", "app")
			s.live["old-custom-name"].SystemLabels[orchestrator.SecretEnabledLabel] = "true"
			cfg := testConfig()
			cfg.Name = "demo"
			cfg.Services = map[string]*config.Service{"app": {Image: "image", ContainerName: "new-custom-name"}}
			s.owned("new-custom-name", "app")
			withTestRuntime(t, s, cfg, func() {
				_, _, err := captureCommand(t, "", func() error { return runDirectStart(context.Background(), commandOptions{}, []string{"app"}, restart) })
				if err != config.ErrSecretUnavailable || len(s.mutations()) != 0 {
					t.Fatalf("marker/name change bypassed: %v %v", err, s.mutations())
				}
			})
		})
	}
}

func TestOrdinaryRestartStopsOnlyRequestedCurrentContainers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{"subset", []string{"app"}, []string{"stop immutable-current-app"}},
		{"explicit dependencies", []string{"app", "dep"}, []string{"stop immutable-current-app", "stop immutable-demo_dep"}},
		{"no deps", []string{"--no-deps", "app"}, []string{"stop immutable-current-app"}},
		{"full stack", nil, []string{"stop immutable-current-app", "stop immutable-demo_dep", "stop immutable-demo_other", "stop immutable-old-app", "stop immutable-orphan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSecretSpy()
			cfg := &config.DillConfig{Name: "demo", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "image", ContainerName: "current-app", DependsOn: []string{"dep"}}, "dep": {Image: "image"}, "other": {Image: "image"}}}
			for name, service := range map[string]string{"current-app": "app", "old-app": "app", "demo_dep": "dep", "demo_other": "other", "orphan": "removed"} {
				s.owned(name, service)
				s.statuses[name] = &orchestrator.ContainerStatus{State: "running"}
			}
			withTestRuntime(t, s, cfg, func() {
				_, _, err := captureCommand(t, "", func() error { return runRestart(context.Background(), commandOptions{Timeout: time.Minute}, tc.args) })
				if err != nil {
					t.Fatal(err)
				}
			})
			stops := []string{}
			for _, event := range s.events {
				if strings.HasPrefix(event, "stop ") {
					stops = append(stops, event)
				}
			}
			sort.Strings(stops)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(stops, tc.want) {
				t.Fatalf("stop set = %v, want %v", stops, tc.want)
			}
			if tc.name == "subset" {
				for _, name := range []string{"old-app", "demo_dep"} {
					if !strings.Contains(strings.Join(s.events, ","), "inspect "+name) {
						t.Fatal("subset narrowed safety census")
					}
				}
			}
		})
	}
}
