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
	"github.com/umayr/dill/internal/plan"
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

type spyCheckedEngine struct {
	orchestrator.Engine
	req   secretfile.Request
	check func(context.Context, secretfile.Request) (*secretfile.Result, error)
}

func (e spyCheckedEngine) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	if _, err := e.check(ctx, e.req); err != nil {
		return "", err
	}
	return e.Engine.StartService(ctx, name, svc, stack)
}

// These spies test CLI operation ordering, not Linux peer authorization. The
// production paths still require opaque evidence and checked adapter creation.
func mockSecretOperation(t *testing.T, s *secretSpy, check func(context.Context, secretfile.Request) (*secretfile.Result, error), kind plan.ChangeKind) {
	t.Helper()
	oldPre, oldPlan, oldWrap := secretPreflight, secretPlanCompute, secretEngineWrap
	t.Cleanup(func() { secretPreflight, secretPlanCompute, secretEngineWrap = oldPre, oldPlan, oldWrap })
	secretPreflight = check
	secretEngineWrap = func(e orchestrator.Engine, r secretfile.Request, _ *secretfile.Result) (orchestrator.Engine, error) {
		return spyCheckedEngine{e, r, check}, nil
	}
	secretPlanCompute = func(_ context.Context, c *config.DillConfig, _ orchestrator.Engine, _ string, _ secretfile.Request, _ *secretfile.Result) (*plan.Plan, error) {
		s.event("plan")
		p := &plan.Plan{}
		for name := range c.Services {
			p.Changes = append(p.Changes, plan.Change{Service: name, Kind: kind})
		}
		p.Changes = append(p.Changes, plan.Change{Service: "demo_orphan", Kind: plan.KindRemove})
		return p, nil
	}
}
func TestSecretUnavailableBlocksEveryMutation(t *testing.T) {
	for _, policy := range []string{"always", "missing"} {
		for _, selection := range [][]string{nil, {"app"}} {
			for _, noDeps := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%v/%t", policy, selection, noDeps), func(t *testing.T) {
					s := newSecretSpy()
					called := false
					mockSecretOperation(t, s, func(_ context.Context, r secretfile.Request) (*secretfile.Result, error) {
						called = true
						if !reflect.DeepEqual(r.Services, selection) || r.NoDeps != noDeps {
							t.Fatal("selection not forwarded")
						}
						return nil, errors.New("synthetic-private-unavailable")
					}, plan.KindRecreate)
					opened := false
					err := runUpConfig(context.Background(), secretOptions(), secretConfig(), selection, noDeps, policy, func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return s, nil })
					if err != config.ErrSecretUnavailable || !called || opened || len(s.mutations()) != 0 {
						t.Fatalf("err=%v opened=%t mutations=%v", err, opened, s.mutations())
					}
				})
			}
		}
	}
}
func TestSecretSubsetClosureNoDepsAndDryRun(t *testing.T) {
	for _, noDeps := range []bool{false, true} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("noDeps=%t/dry=%t", noDeps, dry), func(t *testing.T) {
				s := newSecretSpy()
				checks := 0
				mockSecretOperation(t, s, func(_ context.Context, r secretfile.Request) (*secretfile.Result, error) {
					checks++
					s.event("preflight")
					selected, err := selectServices(r.Config.Services, r.Services, !r.NoDeps)
					if err != nil {
						t.Fatal(err)
					}
					if _, bad := selected["other"]; bad {
						return nil, config.ErrSecretUnavailable
					}
					if len(r.Config.Services["app"].DependsOn) != 1 {
						t.Fatal("no-deps mutated authorization graph")
					}
					return nil, nil
				}, plan.KindCreate)
				opts := secretOptions()
				opts.DryRun = dry
				out, stderr, err := captureCommand(t, "", func() error {
					return runUpConfig(context.Background(), opts, secretConfig(), []string{"app"}, noDeps, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { s.event("open"); return s, nil })
				})
				if err != nil {
					t.Fatal(err)
				}
				if stderr != "" || strings.Contains(out, "synthetic-private") {
					t.Fatal("raw SDK progress leaked")
				}
				if len(s.events) < 2 || s.events[0] != "preflight" || s.events[1] != "open" {
					t.Fatalf("preflight not first: %v", s.events)
				}
				if dry {
					if len(s.mutations()) != 0 || checks != 1 {
						t.Fatal("dry-run mutated or re-created")
					}
					return
				}
				creates := 0
				for _, e := range s.events {
					if strings.HasPrefix(e, "remove ") || strings.HasPrefix(e, "stop ") {
						t.Fatal("subset removed orphan")
					}
					if strings.HasPrefix(e, "create ") {
						creates++
						if e == "create other" || noDeps && e == "create dep" {
							t.Fatal("unselected source/service used")
						}
					}
				}
				want := 2
				if noDeps {
					want = 1
				}
				if creates != want || checks != want+1 {
					t.Fatalf("creates=%d checks=%d events=%v", creates, checks, s.events)
				}
			})
		}
	}
}
func TestSecretOwnershipAndRecheckFailureOrdering(t *testing.T) {
	t.Run("orphan ownership before always pull", func(t *testing.T) {
		s := newSecretSpy()
		s.owned("demo_orphan", "orphan")
		s.live["demo_orphan"].SystemLabels["dill.stack"] = "foreign"
		mockSecretOperation(t, s, func(context.Context, secretfile.Request) (*secretfile.Result, error) { return nil, nil }, plan.KindCreate)
		err := runUpConfig(context.Background(), secretOptions(), secretConfig(), nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return s, nil })
		if err != config.ErrSecretUnavailable || len(s.mutations()) != 0 {
			t.Fatalf("ownership failure mutated: %v %v", err, s.events)
		}
	})
	t.Run("after pull is partial not atomic", func(t *testing.T) {
		s := newSecretSpy()
		calls := 0
		mockSecretOperation(t, s, func(context.Context, secretfile.Request) (*secretfile.Result, error) {
			calls++
			s.event("preflight")
			if calls > 1 {
				return nil, config.ErrSecretUnavailable
			}
			return nil, nil
		}, plan.KindCreate)
		err := runUpConfig(context.Background(), secretOptions(), secretConfig(), []string{"app"}, true, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return s, nil })
		if err != config.ErrSecretUnavailable || calls != 2 {
			t.Fatalf("recheck error=%v calls=%d", err, calls)
		}
		joined := strings.Join(s.mutations(), ",")
		if !strings.Contains(joined, "pull image") || !strings.Contains(joined, "network") || strings.Contains(joined, "create ") {
			t.Fatalf("partial mutation accounting wrong: %s", joined)
		}
	})
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
