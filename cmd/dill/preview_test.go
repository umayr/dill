package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/secretfile"
)

func TestPreviewFlagsRequireBothAndSecretPlan(t *testing.T) {
	oldLoad, oldEngine := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoad, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("invalid preview request contacted engine")
		return nil, nil
	}
	for _, args := range [][]string{{"plan", "--proof-file", "/synthetic/output"}, {"plan", "--proof-key-file", "/synthetic/key"}, {"plan", "--proof-file=", "--proof-key-file="}, {"plan", "--proof-file", "relative", "--proof-key-file", "/synthetic/key"}, {"plan", "--proof-file", "/synthetic/output", "--proof-key-file", "/synthetic/key"}, {"--proof-file", "/synthetic/output", "--proof-key-file", "/synthetic/key", "up"}, {"--proof-file", "/synthetic/output", "--proof-key-file", "/synthetic/key", "diff"}} {
		out, stderr, err := captureCommand(t, "", func() error { return run(args) })
		if err != config.ErrSecretUnavailable || strings.Contains(out+stderr, "synthetic") {
			t.Fatalf("unsafe invalid preview: %v %q %q", err, out, stderr)
		}
	}
}
func TestPreviewCLICompletionDriftAndFailure(t *testing.T) {
	for _, kind := range []plan.ChangeKind{plan.KindNoop, plan.KindRecreate} {
		t.Run(kindName(kind), func(t *testing.T) {
			oldLoad, oldEngine, oldPre, oldCompute, oldPublish := configLoader, engineFactory, secretPreflight, secretPreviewCompute, secretPreviewPublish
			defer func() {
				configLoader, engineFactory, secretPreflight, secretPreviewCompute, secretPreviewPublish = oldLoad, oldEngine, oldPre, oldCompute, oldPublish
			}()
			cfg := secretConfig()
			configLoader = func(context.Context, string) (*config.DillConfig, error) { return cfg, nil }
			engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return newFakeEngine(), nil }
			secretPreflight = func(context.Context, secretfile.Request) (*secretfile.Result, error) { return nil, nil }
			secretPreviewCompute = func(context.Context, *config.DillConfig, orchestrator.Engine, string, secretfile.Request, *secretfile.Result, plan.PreviewOptions) (*plan.Plan, secretfile.PreviewState, error) {
				return &plan.Plan{Changes: []plan.Change{{Service: "app", Kind: kind}}}, secretfile.PreviewState{}, nil
			}
			dir := t.TempDir()
			outPath, keyPath := filepath.Join(dir, "proof"), filepath.Join(dir, "key")
			published, kept := false, false
			// Wiring fixture only: private Linux IO/authorization is tested separately.
			secretPreviewPublish = func(_ secretfile.Request, _ *secretfile.Result, key, out, cfg string, _ secretfile.PreviewState) (func(bool), error) {
				published = true
				if key != keyPath || out != outPath {
					t.Fatal("preview paths lost")
				}
				return func(success bool) { kept = success }, nil
			}
			args := []string{"-p", "demo", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "--secret-target", "lab", "--secret-bindings", "/synthetic/bindings", "plan", "--format", "json", "--proof-key-file", keyPath, "--proof-file", outPath}
			t.Setenv("DILL_SECRET_POLICY", "/synthetic/policy")
			out, _, err := captureCommand(t, "", func() error { return run(args) })
			if !published || !kept || !strings.Contains(out, "changes") || (kind == plan.KindNoop && err != nil) || (kind != plan.KindNoop && !errors.Is(err, errPlanHasChanges)) {
				t.Fatalf("completed plan lost proof: %v", err)
			}
			published = false
			secretPreviewCompute = func(context.Context, *config.DillConfig, orchestrator.Engine, string, secretfile.Request, *secretfile.Result, plan.PreviewOptions) (*plan.Plan, secretfile.PreviewState, error) {
				return nil, secretfile.PreviewState{}, errors.New("synthetic-private-failure")
			}
			_, _, err = captureCommand(t, "", func() error { return run(args) })
			if err != config.ErrSecretUnavailable || published {
				t.Fatal("failed plan published proof")
			}
			if _, err := os.Lstat(outPath); !os.IsNotExist(err) {
				t.Fatal("wiring fixture unexpectedly created a real proof")
			}
			secretPreviewCompute = func(context.Context, *config.DillConfig, orchestrator.Engine, string, secretfile.Request, *secretfile.Result, plan.PreviewOptions) (*plan.Plan, secretfile.PreviewState, error) {
				return &plan.Plan{Changes: []plan.Change{{Service: "app", Kind: kind}}}, secretfile.PreviewState{}, nil
			}
			closed, err := os.CreateTemp(dir, "closed-stdout")
			if err != nil {
				t.Fatal(err)
			}
			closed.Close()
			saved := os.Stdout
			os.Stdout = closed
			published, kept = false, true
			err = run(args)
			os.Stdout = saved
			if err != config.ErrSecretUnavailable || !published || kept {
				t.Fatal("render failure retained newly published proof")
			}

		})
	}
}
func kindName(k plan.ChangeKind) string {
	if k == plan.KindNoop {
		return "noop"
	}
	return "drift"
}
