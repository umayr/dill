package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/secretfile"
)

const syntheticPrivateDiagnostic = "SYNTHETIC-PRIVATE-DIAGNOSTIC"

func TestSecretMainHelper(t *testing.T) {
	mode := os.Getenv("DILL_SECRET_MAIN_TEST")
	if mode == "" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Args = append([]string{"dill"}, os.Args[i+1:]...)
			break
		}
	}
	switch mode {
	case "static":
		configLoader = func(context.Context, string) (*config.DillConfig, error) { panic("static discovery evaluated config") }
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
			panic("static discovery opened engine")
		}
	case "disabled-unknown":
		configLoader = func(context.Context, string) (*config.DillConfig, error) { return nil, config.ErrSecretUnavailable }
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
			panic("unknown secret source opened engine")
		}
	case "disabled-grants", "disabled-requirement":
		configLoader = func(context.Context, string) (*config.DillConfig, error) {
			if mode == "disabled-requirement" {
				c := testConfig()
				c.Secrets = []config.SecretRequirement{{Name: "db"}}
				return c, nil
			}
			return secretConfig(), nil
		}
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
			panic("declared secrets opened engine")
		}
	case "sdk", "sdk-grants":
		configLoader = func(context.Context, string) (*config.DillConfig, error) {
			if mode == "sdk-grants" {
				return secretConfig(), nil
			}
			return testConfig(), nil
		}
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
			logger.Debug(syntheticPrivateDiagnostic)
			logrus.Error(syntheticPrivateDiagnostic)
			return nil, errors.New(syntheticPrivateDiagnostic)
		}
	case "cleanup", "cleanup-unowned":
		configLoader = func(ctx context.Context, path string) (*config.DillConfig, error) {
			os.WriteFile(os.Getenv("DILL_TEST_LOADER_MARKER"), []byte("called"), 0600)
			return config.Load(ctx, path)
		}
		e := newSecretSpy()
		e.owned("demo_app", "app")
		e.live["demo_app"].SystemLabels[orchestrator.SecretEnabledLabel] = "true"
		if mode == "cleanup-unowned" {
			e.live["demo_app"].SystemLabels["dill.managed"] = "false"
		}
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
			return &cleanupRecordingEngine{e}, nil
		}
		secretPreflight = func(context.Context, secretfile.Request) (*secretfile.Result, error) {
			panic("cleanup acquired secret availability")
		}
	case "available", "available-empty":
		configLoader = func(context.Context, string) (*config.DillConfig, error) { return secretConfig(), nil }
		secretPreflight = func(context.Context, secretfile.Request) (*secretfile.Result, error) { return nil, nil }
	default:
		configLoader = func(context.Context, string) (*config.DillConfig, error) {
			logger.Debug(syntheticPrivateDiagnostic)
			logrus.Error(syntheticPrivateDiagnostic)
			return nil, fmt.Errorf("pkl eval: %s", syntheticPrivateDiagnostic)
		}
	}
	main()
}
func secretMain(t *testing.T, mode string, policy bool, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestSecretMainHelper$", "--"}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DILL_SECRET_POLICY=") && !strings.HasPrefix(entry, "DILL_SECRET_MAIN_TEST=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "DILL_SECRET_MAIN_TEST="+mode)
	if policy {
		value := ""
		if mode == "available" {
			value = "/synthetic/policy"
		}
		cmd.Env = append(cmd.Env, "DILL_SECRET_POLICY="+value)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return out.String(), stderr.String(), code
}
func TestSecretValidateAuthorizationOnlyAndTrustedIdentity(t *testing.T) {
	oldLoad, oldPre, oldEngine := configLoader, secretPreflight, engineFactory
	defer func() { configLoader, secretPreflight, engineFactory = oldLoad, oldPre, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return secretConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("validate contacted engine")
		return nil, nil
	}
	checked := false
	secretPreflight = func(_ context.Context, r secretfile.Request) (*secretfile.Result, error) {
		checked = true
		if !r.AuthorizationOnly || r.Target != "lab" || r.Stack != "demo" {
			t.Fatal("validate lost trusted context")
		}
		return nil, nil
	}
	opts := secretOptions()
	out, _, err := captureCommand(t, "", func() error { return runValidate(context.Background(), opts, []string{"--format", "json"}) })
	if err != nil || !checked || strings.TrimSpace(out) != `{"valid":true}` {
		t.Fatalf("validate: %v %q", err, out)
	}
	for _, change := range []func(*commandOptions){func(o *commandOptions) { o.StackName = "foreign" }, func(o *commandOptions) { o.StackName = ""; o.StackNameSet = true }, func(o *commandOptions) { o.Engine = "" }, func(o *commandOptions) { o.EngineSocketSet = false }, func(o *commandOptions) { o.SecretTarget = "" }, func(o *commandOptions) { o.SecretBindingsSet = false }} {
		bad := opts
		change(&bad)
		checked = false
		_, _, err := captureCommand(t, "", func() error { return runValidate(context.Background(), bad, nil) })
		if err != config.ErrSecretUnavailable || checked {
			t.Fatalf("invalid trusted identity accepted: %v", err)
		}
	}
}

// Persist only synthetic engine-call names for subprocess cleanup assertions.
type cleanupRecordingEngine struct{ *secretSpy }

func (e *cleanupRecordingEngine) Close() error {
	return os.WriteFile(os.Getenv("DILL_TEST_CLEANUP_EVENTS"), []byte(strings.Join(e.events, "\n")), 0600)
}

func TestUncheckedPullRejectsGrantsBeforeEngine(t *testing.T) {
	oldLoad, oldEngine := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoad, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return secretConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("unchecked pull opened engine")
		return nil, nil
	}
	if err := run([]string{"pull"}); err != config.ErrSecretUnavailable {
		t.Fatalf("unchecked pull accepted grants: %v", err)
	}
}

func TestStandaloneSecretRequestCanOmitStackOverride(t *testing.T) {
	opts := secretOptions()
	opts.StackName = ""
	opts.StackNameSet = false
	req, err := secretRequest(opts, secretConfig(), nil, false, false)
	if err != nil || req.Stack != "demo" {
		t.Fatal("standalone explicit config.name lost binding/policy identity")
	}
	opts.EngineSocket = "unix:///run/synthetic.sock"
	req, err = secretRequest(opts, secretConfig(), nil, false, false)
	if err != nil || req.Socket != "/run/synthetic.sock" {
		t.Fatal("documented local URI did not bind the canonical policy socket")
	}
	cfg := secretConfig()
	cfg.Name = ""
	if _, err := secretRequest(opts, cfg, nil, false, false); err != config.ErrSecretUnavailable {
		t.Fatal("path-derived secret identity accepted")
	}
}

func TestSecretSelectorsGuardLegacySubcommands(t *testing.T) {
	for _, command := range []string{"init", "fmt", "doctor", "stacks", "ls", "caps", "protocol"} {
		for _, selector := range []string{"environment", "target", "bindings", "proof-key", "proof-output"} {
			t.Run(command+"/"+selector, func(t *testing.T) {
				args := []string{command, "--" + syntheticPrivateDiagnostic}
				switch selector {
				case "target":
					args = append([]string{"--secret-target="}, args...)
				case "bindings":
					args = append([]string{"--secret-bindings="}, args...)
				case "proof-key":
					args = append([]string{"--proof-key-file="}, args...)
				case "proof-output":
					args = append([]string{"--proof-file="}, args...)
				}
				out, stderr, code := secretMain(t, "static", selector == "environment", args...)
				if code != 1 || strings.Contains(out+stderr, syntheticPrivateDiagnostic) || strings.Contains(out+stderr, "Usage:") || !strings.Contains(out+stderr, config.ErrSecretUnavailable.Error()) {
					t.Fatal("selector diagnostic guard failed")
				}
			})
		}
	}
	// A selector after the command must also guard its local parser.
	out, stderr, code := secretMain(t, "static", false, "fmt", "--secret-target=", "--"+syntheticPrivateDiagnostic)
	if code != 1 || out != "" || stderr != config.ErrSecretUnavailable.Error()+"\n" {
		t.Fatal("local selector escaped guard")
	}
	for _, command := range []string{"init", "fmt", "caps", "stacks"} {
		out, stderr, code = secretMain(t, "pkl", false, command, "--"+syntheticPrivateDiagnostic)
		if code != 2 || !strings.Contains(out+stderr, syntheticPrivateDiagnostic) {
			t.Fatal("no-selector legacy parser diagnostics changed")
		}
	}
	for _, policy := range []bool{false, true} {
		args := []string{"caps", "--engine", "docker"}
		if !policy {
			args = append([]string{"--secret-target="}, args...)
		}
		out, stderr, code = secretMain(t, "static", policy, args...)
		if code == 0 || strings.Contains(out+stderr, "docker") {
			t.Fatal("secret selector bypassed disabling")
		}
	}
}

func TestSecretSelectorPreventsFormatterAndDoctorDiagnostics(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "formatter-called")
	pkl := filepath.Join(dir, "pkl")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'Pkl 0.31.1'; exit 0; fi\nprintf called > " + fmt.Sprintf("%q", marker) + "\necho " + syntheticPrivateDiagnostic + " >&2\nexit 1\n"
	if os.WriteFile(pkl, []byte(script), 0700) != nil {
		t.Fatal("formatter fixture")
	}
	t.Setenv("DILL_PKL_PATH", pkl)
	for _, policy := range []bool{false, true} {
		args := []string{"fmt"}
		if !policy {
			args = append([]string{"--secret-target="}, args...)
		}
		out, stderr, code := secretMain(t, "pkl", policy, args...)
		if code != 1 || strings.Contains(out+stderr, syntheticPrivateDiagnostic) {
			t.Fatal("formatter diagnostic escaped guard")
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("guarded formatter executed")
		}
		args = []string{"doctor", "--format", "json"}
		if !policy {
			args = append([]string{"--secret-target="}, args...)
		}
		out, stderr, code = secretMain(t, "sdk", policy, args...)
		if code != 1 || strings.Contains(out+stderr, syntheticPrivateDiagnostic) || strings.Contains(out, "checks") {
			t.Fatal("doctor detail escaped guard")
		}
	}
	out, stderr, code := secretMain(t, "pkl", false, "fmt")
	if code != 1 || !strings.Contains(out+stderr, syntheticPrivateDiagnostic) {
		t.Fatal("legacy formatter stderr changed")
	}
	out, stderr, code = secretMain(t, "sdk", false, "doctor", "--format", "json")
	if code != 1 || !strings.Contains(out+stderr, syntheticPrivateDiagnostic) {
		t.Fatal("legacy doctor details changed")
	}
}
