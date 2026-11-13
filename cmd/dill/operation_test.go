package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

func operationArgs(name, engine string, extra ...string) []string {
	args := []string{name, "--contract", operation.Version, "--engine", engine, "--engine-socket", "/synthetic/SOCKET_SENTINEL.sock", "-p", "app"}
	return append(args, extra...)
}
func captureOperation(t *testing.T, stdin string, args []string) (operation.Result, int, string) {
	t.Helper()
	out, stderr, err := captureCommand(t, stdin, func() error { return runOperation(context.Background(), args) })
	var result operation.Result
	if json.Unmarshal([]byte(out), &result) != nil {
		t.Fatalf("not one JSON result: %q (%v)", out, err)
	}
	if result.Version != operation.Version || !regexp.MustCompile(`^op_[0-9a-f]{32}$`).MatchString(result.OperationID) {
		t.Fatalf("invalid envelope: %+v", result)
	}
	if strings.Contains(out, "SENTINEL") || strings.Contains(stderr, "SENTINEL") {
		t.Fatalf("unsafe output: %s %s", out, stderr)
	}
	return result, exitCodeFor(err), stderr
}

func TestOperationNegotiationBeforeAccess(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader = oldLoader; engineFactory = oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		t.Fatal("evaluated rejected request")
		return nil, nil
	}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("opened rejected request")
		return nil, nil
	}
	for _, args := range [][]string{
		nil, {"pull", "--contract", operation.Version}, {"up", "--contract", "dill.operation/999"},
		operationArgs("up", "docker", "--format", "unsupported"), operationArgs("up", "docker", "--pull", "bad"),
		operationArgs("up", "docker", "--unknown", "SENTINEL"), operationArgs("up", "docker", "--env-file", "/SENTINEL"),
		{"up", "--contract", operation.Version, "--engine", "docker", "-p", "app"},
		operationArgs("validate", "docker", "--stdin"), operationArgs("up", "remote"),
	} {
		result, code, stderr := captureOperation(t, "", args)
		if code != 2 || result.Outcome != "invalid_input" || result.Effects != "none" || stderr != "" {
			t.Fatalf("args %v: %+v, exit %d, stderr %q", args, result, code, stderr)
		}
	}
}

func TestOperationReadOnlyAndLifecycleResults(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, name := range operation.Operations {
			t.Run(engine+"/"+name, func(t *testing.T) {
				f := newFakeEngine()
				if name == "start" || name == "restart" {
					f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
				}
				cfg := testConfig()
				cfg.Engine = engine
				withTestRuntime(t, f, cfg, func() {
					result, code, stderr := captureOperation(t, "", operationArgs(name, engine, "--yes"))
					wantCode, wantOutcome := 0, "succeeded"
					if name == "plan" {
						wantCode, wantOutcome = 1, "changes"
					}
					if name == "diff" {
						wantOutcome = "changes"
					}
					if code != wantCode || result.Outcome != wantOutcome || stderr != "" {
						t.Fatalf("%+v code=%d stderr=%s", result, code, stderr)
					}
					if result.Target == nil || result.Target.Engine != engine || result.Target.EndpointDigest != operation.Digest("unix:///synthetic/SOCKET_SENTINEL.sock") || result.Stack != "app" {
						t.Fatalf("wrong target/stack: %+v", result)
					}
					if name == "validate" || name == "plan" || name == "diff" {
						if result.Effects != "none" || len(f.pulled) > 0 || len(f.removed) > 0 {
							t.Fatalf("read-only effects: %+v", result)
						}
					}
				})
			})
		}
	}
}

func TestOperationInvalidInputNoEngineOrWrites(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		cfg := testConfig()
		cfg.Services["web"].DependsOn = []string{"missing"}
		f := newFakeEngine()
		withTestRuntime(t, f, cfg, func() {
			engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
				t.Fatal("engine opened for invalid input")
				return nil, nil
			}
			result, code, _ := captureOperation(t, "", operationArgs("up", engine))
			if result.Outcome != "invalid_input" || result.Effects != "none" || code != 2 {
				t.Fatalf("%+v code=%d", result, code)
			}
		})
	}
}

func TestOperationForeignCollisionNoPullOrWrite(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, name := range []string{"up", "plan", "diff"} {
			f := &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{"app_web": {ID: "FOREIGN_SENTINEL"}}}
			cfg := testConfig()
			cfg.Services["web"].PullPolicy = "always"
			withTestRuntime(t, f, cfg, func() {
				result, code, _ := captureOperation(t, "", operationArgs(name, engine))
				if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(f.mutations) > 0 || len(f.pulled) > 0 {
					t.Fatalf("%s: %+v mutations=%v pulls=%v", name, result, f.mutations, f.pulled)
				}
			})
		}
	}
}

type operationCensusEngine struct {
	*ownershipEngine
	inspected []string
	closed    int
}

func (e *operationCensusEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.inspected = append(e.inspected, name)
	return e.ownershipEngine.InspectConfig(ctx, name)
}
func (e *operationCensusEngine) Close() error { e.closed++; return nil }
func (e *operationCensusEngine) RemoveImage(context.Context, string, bool) error {
	e.mutations = append(e.mutations, "image")
	return nil
}

func TestOperationEveryLifecycleCensusBeforeFirstWrite(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, name := range []string{"up", "down", "stop", "start", "restart", "rm", "teardown"} {
			for _, foreignOrphan := range []bool{false, true} {
				f := &operationCensusEngine{ownershipEngine: &ownershipEngine{fakeEngine: newFakeEngine(), orphans: []string{"app_a", "app_z"}, live: map[string]*orchestrator.LiveConfig{
					"app_a": {ID: "owned-first", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "a"}},
					"app_z": {ID: "FOREIGN_SENTINEL"},
				}}}
				cfg := testConfig()
				cfg.Services = map[string]*config.Service{"a": {Image: "busybox", PullPolicy: "always"}}
				if !foreignOrphan {
					cfg.Services["z"] = &config.Service{Image: "busybox"}
				}
				withTestRuntime(t, f, cfg, func() {
					opens := 0
					engineFactory = func(_ context.Context, engine, endpoint string) (orchestrator.Engine, error) {
						opens++
						if engine != adapter || endpoint != "unix:///synthetic/SOCKET_SENTINEL.sock" {
							t.Fatal("target changed")
						}
						return f, nil
					}
					extra := []string{"--yes"}
					if name != "teardown" {
						extra = append(extra, "a")
						if !foreignOrphan {
							extra = append(extra, "z")
						}
					}
					result, code, _ := captureOperation(t, "", operationArgs(name, adapter, extra...))
					if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(f.mutations) > 0 || len(f.pulled) > 0 || opens != 1 || f.closed != 1 || !reflect.DeepEqual(f.inspected, []string{"app_a", "app_z"}) {
						t.Fatalf("%s/%s orphan=%v: result=%+v mutations=%v pulls=%v opens=%d closed=%d census=%v", adapter, name, foreignOrphan, result, f.mutations, f.pulled, opens, f.closed, f.inspected)
					}
				})
			}
		}
	}
}

func TestOperationStopCensusRejectsForeignLaterTarget(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		f := &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{
			"app_web":   {ID: "owned-first", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}},
			"app_other": {ID: "FOREIGN_SENTINEL"},
		}}
		cfg := testConfig()
		cfg.Services["other"] = &config.Service{Image: "busybox"}
		withTestRuntime(t, f, cfg, func() {
			result, code, _ := captureOperation(t, "", operationArgs("stop", engine, "web", "other"))
			if code != 1 || result.Effects != "none" || len(f.mutations) != 0 {
				t.Fatalf("mutated before complete census: %+v, %v", result, f.mutations)
			}
		})
	}
}

type operationStopRaceEngine struct {
	*ownershipEngine
	inspected []string
	closed    int
}

func (e *operationStopRaceEngine) Close() error { e.closed++; return nil }

func (e *operationStopRaceEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.inspected = append(e.inspected, name)
	return e.ownershipEngine.InspectConfig(ctx, name)
}
func (e *operationStopRaceEngine) StopService(_ context.Context, id string) error {
	e.mutations = append(e.mutations, "stop:"+id)
	e.live["app_other"].ID = "replacement-id"
	return nil
}
func TestOperationStopUsesCapturedImmutableIDs(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		f := &operationStopRaceEngine{ownershipEngine: &ownershipEngine{fakeEngine: newFakeEngine(), live: map[string]*orchestrator.LiveConfig{
			"app_web":   {ID: "first-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}},
			"app_other": {ID: "second-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "other"}},
		}}}
		cfg := testConfig()
		cfg.Services["other"] = &config.Service{Image: "busybox"}
		withTestRuntime(t, f, cfg, func() {
			result, code, _ := captureOperation(t, "", operationArgs("stop", engine, "web", "other"))
			if code != 0 || result.Effects != "complete" || !reflect.DeepEqual(f.mutations, []string{"stop:first-id", "stop:second-id"}) || !reflect.DeepEqual(f.inspected, []string{"app_other", "app_web", "app_web", "app_other"}) || f.closed != 1 {
				t.Fatalf("census was not immutable: %+v, mutations=%v inspected=%v", result, f.mutations, f.inspected)
			}
		})
	}
}

func TestOperationFailuresCancellationAndDeclinedPrompt(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		f := &failingLifecycleEngine{fakeEngine: newFakeEngine(), failure: errors.New("SDK_SENTINEL")}
		f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
		withTestRuntime(t, f, testConfig(), func() {
			result, code, _ := captureOperation(t, "", operationArgs("stop", engine))
			if result.Outcome != "unknown" || result.Effects != "unknown" || code != 1 || len(result.Resources) != 1 {
				t.Fatalf("%+v code=%d", result, code)
			}
			result, code, stderr := captureOperation(t, "n\n", operationArgs("rm", engine))
			if result.Outcome != "cancelled" || result.Effects != "none" || code != 0 || !strings.Contains(stderr, "Continue?") {
				t.Fatalf("%+v code=%d stderr=%q", result, code, stderr)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, stderr, err := captureCommand(t, "", func() error { return runOperation(ctx, operationArgs("up", "docker")) })
	var result operation.Result
	_ = json.Unmarshal([]byte(out), &result)
	if result.Outcome != "cancelled" || result.Effects != "none" || exitCodeFor(err) != 1 || stderr != "" {
		t.Fatalf("%s %s %v", out, stderr, err)
	}
}

func TestOperationDestructiveIntentDryAndDeclined(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, name := range []string{"down", "rm", "teardown"} {
			for _, dry := range []bool{false, true} {
				if !dry && name == "down" {
					continue
				} // Legacy down has no prompt; do not add one.
				f := &failingLifecycleEngine{fakeEngine: newFakeEngine(), failure: errors.New("MUTATION_SENTINEL")}
				f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
				withTestRuntime(t, f, testConfig(), func() {
					extra := []string{}
					if dry {
						extra = append(extra, "--dry-run")
					}
					result, code, stderr := captureOperation(t, "n\n", operationArgs(name, adapter, extra...))
					want := "cancelled"
					if dry {
						want = "succeeded"
					}
					if code != 0 || result.Outcome != want || result.Effects != "none" || len(result.Resources) == 0 || len(f.calls) > 0 || len(f.removed) > 0 {
						t.Fatalf("%s dry=%v %+v calls=%v", name, dry, result, f.calls)
					}
					for _, resource := range result.Resources {
						if !strings.HasPrefix(resource.Action, "intent.") || resource.Effect != "none" {
							t.Fatalf("intent claimed performed effect: %+v", resource)
						}
					}
					if !dry && (!strings.Contains(stderr, "resource intents") || !strings.Contains(stderr, "sha256:") || !strings.Contains(stderr, "inventoryComplete=true") || !strings.Contains(stderr, "not saved-plan authorization")) {
						t.Fatalf("prompt missing bounded intent: %q", stderr)
					}
				})
			}
		}
	}
	state := &operationState{recorder: &operation.Recorder{}}
	for i := 0; i < 4097; i++ {
		state.recorder.Intent("container", "id", "remove")
	}
	confirmed, err := confirmDestructive("untrusted", commandOptions{operation: state, Yes: true})
	if err == nil || confirmed {
		t.Fatal("incomplete intent accepted even with --yes")
	}
}

func TestOperationRejectsSecrets(t *testing.T) {
	f := newFakeEngine()
	withTestRuntime(t, f, testConfig(), func() {
		old := configLoader
		configLoader = func(context.Context, string) (*config.DillConfig, error) {
			t.Fatal("evaluated identifiable secret request")
			return nil, nil
		}
		result, code, _ := captureOperation(t, "", operationArgs("up", "docker", "--secret-target", "SENTINEL"))
		if code != 1 || result.Errors[0].Code != "SECRET_UNAVAILABLE" || result.Target != nil || result.Stack != "" {
			t.Fatalf("%+v code=%d", result, code)
		}
		configLoader = old
		cfg := testConfig()
		cfg.Services["web"].Secrets = []config.SecretGrant{{}}
		configLoader = func(context.Context, string) (*config.DillConfig, error) { return cfg, nil }
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
			t.Fatal("secret engine opened")
			return nil, nil
		}
		result, code, _ = captureOperation(t, "", operationArgs("up", "docker"))
		if code != 1 || result.Errors[0].Code != "SECRET_UNAVAILABLE" || result.Target != nil || result.Stack != "" {
			t.Fatalf("%+v code=%d", result, code)
		}
	})
}

func TestOperationSharedFixtures(t *testing.T) {
	var manifest struct {
		Fixtures []string `json:"fixtures"`
	}
	data, err := os.ReadFile("../../internal/operation/testdata/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	seenIDs := map[string]bool{}
	for _, file := range manifest.Fixtures {
		t.Run(file, func(t *testing.T) {
			var fixture struct {
				Runtime      string           `json:"runtime"`
				Args         []string         `json:"args"`
				ExpectedExit int              `json:"expectedExit"`
				Expected     operation.Result `json:"expected"`
			}
			data, err := os.ReadFile(filepath.Join("../../internal/operation/testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			withTestRuntime(t, newFakeEngine(), testConfig(), func() {
				engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
					t.Fatal("fixture opened engine")
					return nil, nil
				}
				switch fixture.Runtime {
				case "must-not-access":
					configLoader = func(context.Context, string) (*config.DillConfig, error) {
						t.Fatal("rejected fixture evaluated config")
						return nil, nil
					}
				case "evaluator-failure":
					configLoader = func(context.Context, string) (*config.DillConfig, error) {
						return nil, &config.EvaluatorError{Err: errors.New("EVALUATOR_SENTINEL")}
					}
				}
				result, code, _ := captureOperation(t, "", fixture.Args)
				if seenIDs[result.OperationID] {
					t.Fatal("reused operation ID")
				}
				seenIDs[result.OperationID] = true
				result.OperationID = "op_00000000000000000000000000000000"
				if code != fixture.ExpectedExit || !reflect.DeepEqual(result, fixture.Expected) {
					t.Fatalf("got %+v exit %d; want %+v exit %d", result, code, fixture.Expected, fixture.ExpectedExit)
				}
			})
		})
	}
}

type operationPreviewEngine struct{ *endpointFake }

type operationDisposableEngine struct {
	*operationPreviewEngine
	enforcementError error
	enabled          bool
}

func (e *operationDisposableEngine) EnforceDisposableVolumes() error {
	e.enabled = true
	return e.enforcementError
}

type operationImageInspectorEngine struct {
	*operationPreviewEngine
	failure error
}

func (e *operationImageInspectorEngine) DisposableImageVolumes(context.Context, string) ([]string, error) {
	return []string{"/image-volume"}, e.failure
}

func TestOperationDecoratorPreservesPreparedImageInspection(t *testing.T) {
	plain := &operationPreviewEngine{&endpointFake{fakeEngine: newFakeEngine()}}
	wrapped := &operation.Engine{Engine: plain, Recorder: &operation.Recorder{}, Adapter: "docker"}
	if _, err := wrapped.DisposableImageVolumes(context.Background(), "image"); err == nil {
		t.Fatal("missing image inventory accepted")
	}
	failure := errors.New("metadata unavailable")
	inner := &operationImageInspectorEngine{operationPreviewEngine: plain, failure: failure}
	wrapped.Engine = inner
	if _, err := wrapped.DisposableImageVolumes(context.Background(), "image"); err != failure {
		t.Fatal("metadata error not forwarded")
	}
	inner.failure = nil
	volumes, err := wrapped.DisposableImageVolumes(context.Background(), "image")
	if err != nil || !reflect.DeepEqual(volumes, []string{"/image-volume"}) || len(plain.pulled) != 0 {
		t.Fatalf("inspection not read-only: %v %v", volumes, err)
	}
}

func TestOperationDecoratorRequiresDisposableCapability(t *testing.T) {
	plain := &operationPreviewEngine{&endpointFake{fakeEngine: newFakeEngine()}}
	wrapped := &operation.Engine{Engine: plain, Recorder: &operation.Recorder{}, Adapter: "docker"}
	if wrapped.EnforceDisposableVolumes() == nil {
		t.Fatal("missing capability silently accepted")
	}
	failure := errors.New("synthetic enforcement failure")
	inner := &operationDisposableEngine{operationPreviewEngine: plain, enforcementError: failure}
	wrapped.Engine = inner
	if wrapped.EnforceDisposableVolumes() != failure || !inner.enabled {
		t.Fatal("enforcement error/intent not forwarded")
	}
	inner.enforcementError = nil
	if wrapped.EnforceDisposableVolumes() != nil {
		t.Fatal("supported enforcement rejected")
	}
	binary, _ := engineCommand(wrapped, "inspect", "id")
	if binary != "docker" {
		t.Fatal(binary)
	}
}

func (e *operationPreviewEngine) PreviewResources(context.Context, string, []string) ([]orchestrator.PreviewResource, error) {
	return []orchestrator.PreviewResource{{}}, nil
}

func TestOperationDecoratorPreservesAdapterIdentityAndInspection(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		underlying := &operationPreviewEngine{&endpointFake{fakeEngine: newFakeEngine()}}
		wrapped := &operation.Engine{Engine: underlying, Recorder: &operation.Recorder{}, Adapter: adapter}
		binary, args := engineCommand(wrapped, "inspect", "id")
		flag := "--host"
		if adapter == "podman" {
			flag = "--url"
		}
		if binary != adapter || !reflect.DeepEqual(args, []string{flag, "unix:///test/selected.sock", "inspect", "id"}) {
			t.Fatalf("%s %v", binary, args)
		}
		facts, err := wrapped.PreviewResources(context.Background(), "app", nil)
		if err != nil || len(facts) != 1 {
			t.Fatalf("lost optional inspection: %v %v", facts, err)
		}
	}
}

func TestOperationCapabilitiesAndRootDispatch(t *testing.T) {
	out, stderr, err := captureCommand(t, "", func() error { return run([]string{"operation-capabilities"}) })
	if err != nil || stderr != "" || !strings.Contains(out, operation.Version) || strings.Contains(out, "protocol.check") {
		t.Fatalf("%s %s %v", out, stderr, err)
	}
	// Ensure fixture distribution is available to downstream consumers.
	if _, err := os.Stat("../../internal/operation/testdata/manifest.json"); err != nil {
		t.Fatal(err)
	}
}
