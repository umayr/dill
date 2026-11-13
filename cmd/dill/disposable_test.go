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

type disposableUpEngine struct {
	*preMutationEngine
	facts   []orchestrator.PreviewResource
	failure error
}

func (e *disposableUpEngine) EnforceDisposableVolumes() error { return nil }
func (e *disposableUpEngine) DisposableImage(context.Context, string) (string, []string, error) {
	return "prepared-image", nil, nil
}
func (e *disposableUpEngine) PreviewResources(context.Context, string, []string) ([]orchestrator.PreviewResource, error) {
	return e.facts, e.failure
}
func disposableOptions() commandOptions {
	return commandOptions{Engine: "docker", EngineSocket: "/synthetic/disposable.sock", EngineSocketSet: true, StackName: "test", StackNameSet: true, AlphaDisposable: true, Quiet: true, Timeout: time.Second}
}
func disposableConfig() *config.DillConfig {
	return &config.DillConfig{Name: "test", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "synthetic", PullPolicy: "never", Volumes: []json.RawMessage{json.RawMessage(`"data:/data"`)}}}}
}
func newDisposableUpEngine() *disposableUpEngine {
	return &disposableUpEngine{preMutationEngine: &preMutationEngine{recordingUpEngine: &recordingUpEngine{fakeEngine: newFakeEngine()}, live: map[string]*orchestrator.LiveConfig{"test_orphan": ownedPreMutationOrphan()}}, facts: []orchestrator.PreviewResource{{Kind: "network", Name: "test"}, {Kind: "volume", Name: "test_data"}}}
}

func TestFullUpNoPruneRetainsOmittedServices(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, noPrune := range []bool{false, true} {
			t.Run(engine+map[bool]string{false: "/legacy-prune", true: "/no-prune"}[noPrune], func(t *testing.T) {
				e := newDisposableUpEngine().preMutationEngine
				cfg := disposableConfig()
				cfg.Engine = engine
				err := runUpConfig(context.Background(), commandOptions{NoPrune: noPrune, Quiet: true, Timeout: time.Second}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
				if err != nil {
					t.Fatal(err)
				}
				events := strings.Join(e.events, ",")
				removed := strings.Contains(events, "remove:orphan-id")
				if removed == noPrune || strings.Contains(events, "remove-volume") {
					t.Fatalf("noPrune=%t mutations=%v", noPrune, e.events)
				}
			})
		}
	}
}

func TestDisposableProfileImpliesNoPruneDespiteFalseFlag(t *testing.T) {
	for _, args := range [][]string{{"--alpha-disposable", "--no-prune=false"}, {"--alpha-disposable", "--no-prune=false", "--no-deps", "app"}} {
		e := newDisposableUpEngine()
		withTestRuntime(t, e, disposableConfig(), func() {
			err := runUp(context.Background(), disposableOptions(), args)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range e.events {
				if strings.HasPrefix(event, "remove:") || strings.HasPrefix(event, "stop:") {
					t.Fatalf("implicit prune: %v", e.events)
				}
			}
		})
	}
}

func TestNoPruneStillRejectsForeignDesiredContainer(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		e := newDisposableUpEngine().preMutationEngine
		e.live["test_app"] = &orchestrator.LiveConfig{ID: "foreign"}
		cfg := disposableConfig()
		cfg.Engine = engine
		err := runUpConfig(context.Background(), commandOptions{NoPrune: true, Quiet: true}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
		if !errors.Is(err, orchestrator.ErrOwnershipConflict) || len(e.events) != 0 {
			t.Fatalf("%s: %v mutations=%v", engine, err, e.events)
		}
	}
}

func TestDisposableConfigFailureBeforeEngineOpen(t *testing.T) {
	for _, mount := range []string{`"/inaccessible/missing:/data"`, `"/etc:/data"`, `"data:/data"`} {
		for _, bad := range []string{"mount", "host-network", "devices", "capabilities", "secrets", "implicit-target"} {
			t.Run(bad+mount, func(t *testing.T) {
				cfg, opts := disposableConfig(), disposableOptions()
				svc := cfg.Services["app"]
				switch bad {
				case "mount":
					if mount == `"data:/data"` {
						svc.Volumes = []json.RawMessage{json.RawMessage(`{"type":"volume","source":"data","target":"/data","externalVolume":true}`)}
					} else {
						svc.Volumes = []json.RawMessage{json.RawMessage(mount)}
					}
				case "host-network":
					svc.NetworkMode = "host"
				case "devices":
					svc.Devices = []string{"/dev/fake"}
				case "capabilities":
					svc.CapAdd = []string{"SYS_ADMIN"}
				case "secrets":
					cfg.Secrets = []config.SecretRequirement{{Name: "synthetic"}}
				case "implicit-target":
					opts.EngineSocketSet = false
				}
				opened := false
				err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return nil, nil })
				if err == nil || opened {
					t.Fatalf("error=%v engine opened=%t", err, opened)
				}
			})
		}
	}
}

func TestDisposablePullNeverOverrideSupportsServiceCopies(t *testing.T) {
	for _, args := range [][]string{{"--alpha-disposable", "--pull", "never"}, {"--alpha-disposable", "--pull", "never", "--no-deps", "app"}} {
		e := newDisposableUpEngine()
		cfg := disposableConfig()
		cfg.Services["app"].PullPolicy = "always"
		withTestRuntime(t, e, cfg, func() {
			if err := runUp(context.Background(), disposableOptions(), args); err != nil {
				t.Fatal(err)
			}
			for _, event := range e.events {
				if strings.HasPrefix(event, "pull:") {
					t.Fatalf("prepared profile pulled: %v", e.events)
				}
			}
		})
	}
}

func TestDisposablePullOverrideRejectedBeforeEngine(t *testing.T) {
	for _, policy := range []string{"always", "missing"} {
		opened := false
		err := runUpConfig(context.Background(), disposableOptions(), disposableConfig(), nil, true, policy, func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return nil, nil })
		if !errors.Is(err, orchestrator.ErrDisposableUnsupported) || opened {
			t.Fatalf("policy=%s error=%v opened=%t", policy, err, opened)
		}
	}
}

func TestDisposableResourceFailureHasZeroMutations(t *testing.T) {
	for _, bad := range []string{"foreign-volume", "volume-driver", "volume-options", "foreign-network", "missing-evidence", "raw-error"} {
		t.Run(bad, func(t *testing.T) {
			e := newDisposableUpEngine()
			switch bad {
			case "foreign-volume":
				e.facts[1].Exists = true
				e.facts[1].Settings = json.RawMessage(`{"Driver":"local","Labels":{"dill.managed":"true","dill.stack":"foreign"}}`)
			case "volume-driver":
				e.facts[1].Exists = true
				e.facts[1].Settings = json.RawMessage(`{"Driver":"nfs","Labels":{"dill.managed":"true","dill.stack":"test"}}`)
			case "volume-options":
				e.facts[1].Exists = true
				e.facts[1].Settings = json.RawMessage(`{"Driver":"local","Labels":{"dill.managed":"true","dill.stack":"test"},"Options":{"device":"/production/path"}}`)
			case "foreign-network":
				e.facts[0].Exists = true
				e.facts[0].ID = "foreign"
				e.facts[0].Settings = json.RawMessage(`{"Driver":"bridge"}`)
			case "missing-evidence":
				e.facts = nil
			case "raw-error":
				e.failure = errors.New("PRIVATE_SENTINEL")
			}
			err := runUpConfig(context.Background(), disposableOptions(), disposableConfig(), nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
			if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") || strings.Contains(err.Error(), "/production/path") || len(e.events) != 0 {
				t.Fatalf("error=%v mutations=%v", err, e.events)
			}
		})
	}
}
