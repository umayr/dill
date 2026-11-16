package main

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestDisabledEntryPointsNoEvaluation(t *testing.T) {
	fixed := config.ErrSecretUnavailable.Error() + "\n"
	for _, tc := range []struct {
		args        []string
		policy      bool
		code        int
		out, stderr string
	}{
		{[]string{"up", "--secret-target", "synthetic"}, false, 1, "", fixed},
		{[]string{"plan", "--proof-file", "/synthetic"}, false, 1, "", fixed},
		{[]string{"secrets", "check"}, false, 1, secretUnavailableJSON + "\n", ""},
		{[]string{"up", "--alpha-disposable"}, false, 2, "", "dill: Alpha disposable profile is disabled\n"},
		{[]string{"up", "--alpha-disposable=false"}, false, 2, "", "dill: Alpha disposable profile is disabled\n"},
		{[]string{"secret-capabilities"}, false, 1, "", fixed},
		{[]string{"up"}, true, 1, "", fixed},
	} {
		// The static helper panics if the evaluator or engine is accessed.
		out, stderr, code := secretMain(t, "static", tc.policy, tc.args...)
		if code != tc.code || out != tc.out || stderr != tc.stderr {
			t.Fatalf("%v: code=%d out=%q stderr=%q", tc.args, code, out, stderr)
		}
	}
}

func TestDisabledOperationAlphaBeforeEngine(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		t.Fatal("operation evaluated config")
		return nil, nil
	}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("operation opened engine")
		return nil, nil
	}
	for _, flag := range []string{"--alpha-disposable", "--alpha-disposable=false"} {
		result, code, stderr := captureOperation(t, "", operationArgs("up", "docker", "-p", "test", flag))
		if code != 2 || result.Outcome != "invalid_input" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "UNSUPPORTED_OPTIONS" || stderr != "" {
			t.Fatalf("%s: result=%+v code=%d stderr=%q", flag, result, code, stderr)
		}
	}
	result, code, stderr := captureOperation(t, "", operationArgs("up", "docker", "-p", "test", "--secret-target", "synthetic"))
	if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "SECRET_UNAVAILABLE" || stderr != "" {
		t.Fatalf("secret selector: result=%+v code=%d stderr=%q", result, code, stderr)
	}
}

func TestDisabledDeclaredSecretsBeforeEngine(t *testing.T) {
	for _, mode := range []string{"disabled-grants", "disabled-requirement"} {
		for _, command := range []string{"up", "plan", "down", "stop", "teardown", "validate", "doctor"} {
			// The helper evaluates a declaration and panics if an engine is opened.
			out, stderr, code := secretMain(t, mode, false, "-p", "demo", "--engine", "docker", command)
			if code != 1 || out != "" || stderr != config.ErrSecretUnavailable.Error()+"\n" {
				t.Fatalf("%s/%s: code=%d out=%q stderr=%q", mode, command, code, out, stderr)
			}
		}
	}
}

func TestDisabledHistoryCannotBecomeOrdinaryUp(t *testing.T) {
	for _, name := range []string{"demo_app", "old-custom-name"} {
		for _, marker := range []string{"true", "false", "malformed"} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				s := newSecretSpy()
				s.owned(name, "app")
				s.live[name].SystemLabels[orchestrator.SecretEnabledLabel] = marker
				cfg := &config.DillConfig{Name: "demo", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "image", PullPolicy: "always"}}}
				err := runUpConfig(context.Background(), commandOptions{Quiet: true}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return s, nil })
				if !errors.Is(err, config.ErrSecretUnavailable) || len(s.mutations()) != 0 {
					t.Fatalf("err=%v events=%v", err, s.events)
				}
			})
		}
	}
}

func TestDisabledDeclaredRequirementWithoutGrant(t *testing.T) {
	cfg := testConfig()
	cfg.Secrets = []config.SecretRequirement{{Name: "db"}}
	withTestRuntime(t, newSecretSpy(), cfg, func() {
		for _, command := range []string{"up", "down", "stop"} {
			err := run([]string{"-p", "demo", "--engine", "docker", command})
			if !errors.Is(err, config.ErrSecretUnavailable) {
				t.Fatalf("%s: %v", command, err)
			}
		}
	})
}

func TestDisabledUnknownSecretClassificationNeverFallsBack(t *testing.T) {
	for _, command := range []string{"down", "stop", "up", "doctor"} {
		out, stderr, code := secretMain(t, "disabled-unknown", false, "-p", "demo", "--engine", "docker", command)
		if code != 1 || out != "" || stderr != config.ErrSecretUnavailable.Error()+"\n" {
			t.Fatalf("%s: %d %q", command, code, out+stderr)
		}
	}
}

type unreadableOrphan struct {
	*secretSpy
	inspectErr error
}

func (s unreadableOrphan) ListStack(context.Context, string) ([]string, error) {
	return []string{"demo_unknown"}, nil
}
func (s unreadableOrphan) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	if name == "demo_unknown" && s.inspectErr != nil {
		return nil, s.inspectErr
	}
	return s.secretSpy.InspectConfig(ctx, name)
}
func TestDisabledUnknownOrphanCannotBePruned(t *testing.T) {
	s := newSecretSpy()
	cfg := &config.DillConfig{Name: "demo", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "image", PullPolicy: "always"}}}
	err := runUpConfig(context.Background(), commandOptions{Quiet: true}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) {
		return unreadableOrphan{secretSpy: s}, nil
	})
	if !errors.Is(err, orchestrator.ErrNotFound) || len(s.mutations()) != 0 {
		t.Fatalf("err=%v events=%v", err, s.events)
	}
}

func TestUnknownOrphanInspectionErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, context.Canceled.Error()},
		{context.DeadlineExceeded, context.DeadlineExceeded.Error()},
		{orchestrator.ErrNotFound, "stack container inspection: " + orchestrator.ErrNotFound.Error()},
		{fs.ErrPermission, "stack container inspection: " + fs.ErrPermission.Error()},
		{errors.New("private engine diagnostic"), "stack container inspection unavailable"},
	} {
		s := newSecretSpy()
		cfg := &config.DillConfig{Name: "demo", Engine: "docker", Services: map[string]*config.Service{"app": {Image: "image", PullPolicy: "always"}}}
		err := runUpConfig(context.Background(), commandOptions{Quiet: true}, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) {
			return unreadableOrphan{secretSpy: s, inspectErr: tc.err}, nil
		})
		if err == nil || err.Error() != "computing plan: "+tc.want || len(s.mutations()) != 0 {
			t.Fatalf("input=%v err=%v events=%v", tc.err, err, s.events)
		}
		if (tc.err == context.Canceled || tc.err == context.DeadlineExceeded || tc.err == orchestrator.ErrNotFound || tc.err == fs.ErrPermission) && !errors.Is(err, tc.err) {
			t.Fatalf("context classification lost: %v", err)
		}
	}
}
