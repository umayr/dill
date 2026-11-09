package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"

	"github.com/umayr/dill/internal/orchestrator"
)

type failingLifecycleEngine struct {
	*fakeEngine
	failure error
	calls   []string
}

func (f *failingLifecycleEngine) StopService(_ context.Context, id string) error {
	f.calls = append(f.calls, "stop "+id)
	return f.failure
}
func (f *failingLifecycleEngine) RemoveService(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove "+id)
	return f.failure
}
func (f *failingLifecycleEngine) RemoveNetwork(_ context.Context, id string) error {
	f.calls = append(f.calls, "network "+id)
	return f.failure
}
func (f *failingLifecycleEngine) ListStackVolumes(context.Context, string) ([]string, error) {
	return []string{"vol"}, nil
}
func (f *failingLifecycleEngine) RemoveVolume(_ context.Context, id, stack string, _ bool) error {
	f.calls = append(f.calls, "volume "+id+" "+stack)
	return f.failure
}
func (f *failingLifecycleEngine) LocalImageID(_ context.Context, id string) (string, error) {
	return id + "-id", nil
}
func (f *failingLifecycleEngine) RemoveImage(_ context.Context, id string, _ bool) error {
	f.calls = append(f.calls, "image "+id)
	return f.failure
}
func TestTeardownVolumePromptAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry=%t", dry), func(t *testing.T) {
			f := &failingLifecycleEngine{fakeEngine: newFakeEngine(), failure: errors.New("must not mutate")}
			withTestRuntime(t, f, testConfig(), func() {
				stdout, stderr, err := captureCommand(t, "n\n", func() error {
					return runTeardown(context.Background(), commandOptions{ConfigFile: "compose.pkl", DryRun: dry}, nil)
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(f.calls) != 0 {
					t.Fatalf("unexpected mutations: %v", f.calls)
				}
				if dry && !strings.Contains(stdout, "would remove volumes: vol") {
					t.Fatalf("dry-run %q", stdout)
				}
				if !dry && (!strings.Contains(stderr, "1 named volume(s)") || !strings.Contains(stderr, "vol")) {
					t.Fatalf("prompt %q", stderr)
				}
			})
		})
	}
}

func TestOrphanFailuresAbortDeployment(t *testing.T) {
	f := &failingLifecycleEngine{fakeEngine: newFakeEngine(), failure: errors.New("orphan failure")}
	f.statuses["app_old"] = &orchestrator.ContainerStatus{Name: "app_old", State: "exited"}
	cfg := &config.DillConfig{Name: "app", Services: map[string]*config.Service{"web": {Image: "busybox", PullPolicy: "never"}}}
	err := runUpConfig(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return f, nil })
	if !errors.Is(err, f.failure) || !strings.Contains(err.Error(), "app_old") {
		t.Fatalf("missing orphan failure: %v", err)
	}
	if len(f.calls) != 2 || f.calls[0] != "stop app_old" || f.calls[1] != "remove app_old" {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestLifecycleFailuresAreReturned(t *testing.T) {
	for _, name := range []string{"down", "subset-down", "stop", "rm", "teardown"} {
		t.Run(name, func(t *testing.T) {
			f := &failingLifecycleEngine{fakeEngine: newFakeEngine(), failure: errors.New("injected failure")}
			f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
			withTestRuntime(t, f, testConfig(), func() {
				opts := commandOptions{ConfigFile: "compose.pkl", Yes: true, Quiet: true}
				var err error
				switch name {
				case "down":
					err = runDown(context.Background(), opts, nil)
				case "subset-down":
					err = runDown(context.Background(), opts, []string{"web"})
				case "stop":
					err = runStop(context.Background(), opts, nil)
				case "rm":
					err = runRm(context.Background(), opts, nil)
				case "teardown":
					err = runTeardown(context.Background(), opts, nil)
				}
				if !errors.Is(err, f.failure) {
					t.Fatalf("lost error: %v", err)
				}
				if !strings.Contains(err.Error(), "app_web") {
					t.Fatalf("missing identity: %v", err)
				}
				if name == "teardown" {
					if !strings.Contains(strings.Join(f.calls, ","), "volume vol app") {
						t.Fatalf("missing stack-bound volume removal: %v", f.calls)
					}
					for _, id := range []string{"vol", "busybox", "app"} {
						if !strings.Contains(err.Error(), id) {
							t.Errorf("missing %s: %v", id, err)
						}
					}
				}
			})
		})
	}
}
