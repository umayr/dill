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

type distinctLifecycleFailures struct {
	*failingLifecycleEngine
	stopErr, removeErr, networkErr, volumeErr, imageErr error
}

func (f *distinctLifecycleFailures) StopService(_ context.Context, id string) error {
	f.calls = append(f.calls, "stop "+id)
	return f.stopErr
}
func (f *distinctLifecycleFailures) RemoveService(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove "+id)
	return f.removeErr
}
func (f *distinctLifecycleFailures) RemoveNetwork(_ context.Context, id string) error {
	f.calls = append(f.calls, "network "+id)
	return f.networkErr
}
func (f *distinctLifecycleFailures) RemoveVolume(_ context.Context, id, stack string, _ bool) error {
	f.calls = append(f.calls, "volume "+id)
	return f.volumeErr
}
func (f *distinctLifecycleFailures) RemoveImage(_ context.Context, id string, _ bool) error {
	f.calls = append(f.calls, "image "+id)
	return f.imageErr
}

func TestTeardownAggregatesDistinctFailuresAndContinues(t *testing.T) {
	f := &distinctLifecycleFailures{
		failingLifecycleEngine: &failingLifecycleEngine{fakeEngine: newFakeEngine()},
		stopErr:                errors.New("stop denied"), removeErr: errors.New("remove denied"),
		networkErr: errors.New("network denied"), volumeErr: errors.New("volume denied"), imageErr: errors.New("image denied"),
	}
	f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
	withTestRuntime(t, f, testConfig(), func() {
		err := runTeardown(context.Background(), commandOptions{ConfigFile: "compose.pkl", Yes: true, Quiet: true}, nil)
		for _, cause := range []error{f.stopErr, f.removeErr, f.networkErr, f.volumeErr, f.imageErr} {
			if !errors.Is(err, cause) {
				t.Errorf("missing %v: %v", cause, err)
			}
		}
		for _, identity := range []string{"app_web", "app", "vol", "busybox-id"} {
			if err == nil || !strings.Contains(err.Error(), identity) {
				t.Errorf("missing identity %q: %v", identity, err)
			}
		}
	})
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
				if dry && (!strings.Contains(stdout, "would retain volumes: vol") || !strings.Contains(stdout, "retain the network")) {
					t.Fatalf("dry-run %q", stdout)
				}
				if !dry && !strings.Contains(stderr, "retain the network") {
					t.Fatalf("network prompt %q", stderr)
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

type failedImageInspectEngine struct {
	*failingLifecycleEngine
	inspectErr error
}

func (f *failedImageInspectEngine) LocalImageID(_ context.Context, image string) (string, error) {
	f.calls = append(f.calls, "inspect image "+image)
	return "", f.inspectErr
}

func TestTeardownReportsImageInspectFailureAndContinues(t *testing.T) {
	f := &failedImageInspectEngine{
		failingLifecycleEngine: &failingLifecycleEngine{fakeEngine: newFakeEngine()},
		inspectErr:             errors.New("empty image ID; image may be retained"),
	}
	f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
	withTestRuntime(t, f, testConfig(), func() {
		err := runTeardown(context.Background(), commandOptions{ConfigFile: "compose.pkl", Yes: true, Quiet: true}, nil)
		if !errors.Is(err, f.inspectErr) || !strings.Contains(err.Error(), "busybox") {
			t.Fatalf("missing image inspect failure: %v", err)
		}
		calls := strings.Join(f.calls, ",")
		for _, want := range []string{"remove app_web", "network app", "volume vol app", "inspect image busybox"} {
			if !strings.Contains(calls, want) {
				t.Errorf("missing %q in %s", want, calls)
			}
		}
		for _, call := range f.calls {
			if strings.HasPrefix(call, "image ") {
				t.Errorf("removed image without an ID: %s", calls)
			}
		}
	})
}
