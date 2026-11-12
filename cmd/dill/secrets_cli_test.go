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
func TestSecretSafeErrorsBeforeParsingEvaluationAndSDK(t *testing.T) {
	for _, args := range [][]string{
		{"--secret-bindings=", "--unknown=" + syntheticPrivateDiagnostic},
		{"--secret-bindings=", "--help"},
		{"--secret-target=", "up", "--unknown=" + syntheticPrivateDiagnostic},
		{"--proof-file=" + syntheticPrivateDiagnostic, "plan"},
		{"up", "--secret-bindings=", "-f", syntheticPrivateDiagnostic},
		{"plan", "--secret-target=lab", "--format", "json"},
		{"validate", "--secret-bindings=", "--format", "json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, stderr, code := secretMain(t, "pkl", false, append([]string{"-vvvv"}, args...)...)
			if code != 1 || strings.Contains(out+stderr, syntheticPrivateDiagnostic) || strings.Contains(out+stderr, "flag provided") || strings.Contains(out+stderr, "Usage:") {
				t.Fatalf("unsafe failure code=%d stdout=%q stderr=%q", code, out, stderr)
			}
			if !strings.Contains(out+stderr, config.ErrSecretUnavailable.Error()) {
				t.Fatal("missing fixed error")
			}
		})
	}
	out, stderr, code := secretMain(t, "sdk", true, "-vvvv", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "plan", "--format", "json")
	if code != 1 || strings.Contains(out+stderr, syntheticPrivateDiagnostic) || !strings.Contains(out, config.ErrSecretUnavailable.Error()) {
		t.Fatalf("SDK failure leaked: %d %s %s", code, out, stderr)
	}
	out, stderr, code = secretMain(t, "sdk-grants", false, "-vvvv", "down")
	if code != 1 || strings.Contains(out+stderr, syntheticPrivateDiagnostic) || !strings.Contains(out+stderr, config.ErrSecretUnavailable.Error()) {
		t.Fatal("evaluated cleanup grants did not enable safe diagnostics")
	}
	out, stderr, code = secretMain(t, "pkl", false, "-f", "secrets", "plan")
	if code != 1 || !strings.Contains(out+stderr, syntheticPrivateDiagnostic) {
		t.Fatal("legacy no-selector diagnostics were lost")
	}
}
func TestSecretCapabilitiesAndCheckExactJSON(t *testing.T) {
	out, stderr, code := secretMain(t, "static", true, "secret-capabilities", "--format", "json")
	if code != 0 || out != secretCapabilitiesJSON+"\n" || stderr != "" {
		t.Fatalf("discovery: %d %q %q", code, out, stderr)
	}
	for _, args := range [][]string{{"secrets"}, {"secrets", "other"}, {"secrets", "check", "--unknown=" + syntheticPrivateDiagnostic}, {"--secret-bindings=", "secrets", "check"}} {
		out, stderr, code = secretMain(t, "pkl", false, append([]string{"-vvvv"}, args...)...)
		if code != 1 || out != secretUnavailableJSON+"\n" || stderr != "" {
			t.Fatalf("unavailable: %d %q %q", code, out, stderr)
		}
	}
	// Status wire fixture only; this does not claim Linux runtime availability.
	out, stderr, code = secretMain(t, "available", true, "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "-p", "demo", "secrets", "check", "--secret-target", "lab", "--secret-bindings", "/synthetic/bindings")
	if code != 0 || out != secretAvailableJSON+"\n" || stderr != "" {
		t.Fatalf("available response: %d %q %q", code, out, stderr)
	}
	out, stderr, code = secretMain(t, "available-empty", true, "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "-p", "demo", "secrets", "check", "--secret-target", "lab", "--secret-bindings", "/synthetic/bindings")
	if code != 1 || out != secretUnavailableJSON+"\n" || stderr != "" {
		t.Fatalf("empty policy accepted: %d %q %q", code, out, stderr)
	}
	if secretCheckInvocation([]string{"--secret-bindings", "secrets", "plan"}) {
		t.Fatal("binding filename misclassified as a command")
	}
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
func TestTargetOnlyOrdinaryValidatePlanAndImportedGrants(t *testing.T) {
	oldLoad, oldPre, oldEngine := configLoader, secretPreflight, engineFactory
	defer func() { configLoader, secretPreflight, engineFactory = oldLoad, oldPre, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	secretPreflight = func(context.Context, secretfile.Request) (*secretfile.Result, error) {
		t.Fatal("ordinary target-only operation required binding/source availability")
		return nil, nil
	}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return newFakeEngine(), nil }
	for _, command := range []string{"validate", "plan", "diff"} {
		_, _, err := captureCommand(t, "", func() error {
			return run([]string{"-p", "app", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "--secret-target", "lab", command, "--format", "json"})
		})
		if err != nil && !errors.Is(err, errPlanHasChanges) {
			t.Fatalf("ordinary %s: %v", command, err)
		}
	}
	configLoader = oldLoad
	imported, err := filepath.Abs("../../examples/secrets/compose.pkl")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "imported.pkl")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("amends %q\n", imported)), 0600); err != nil {
		t.Fatal(err)
	}
	opened := false
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		opened = true
		return newFakeEngine(), nil
	}
	// Actual Pkl import evaluation, not text scanning for a local secrets key.
	_, _, err = captureCommand(t, "", func() error {
		return run([]string{"-f", cfg, "-p", "demo", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "--secret-target", "lab", "up"})
	})
	if err != config.ErrSecretUnavailable || opened {
		t.Fatalf("imported grants bypassed missing binding: %v opened=%t", err, opened)
	}
}

// Persist only synthetic engine-call names for subprocess cleanup assertions.
type cleanupRecordingEngine struct{ *secretSpy }

func (e *cleanupRecordingEngine) Close() error {
	return os.WriteFile(os.Getenv("DILL_TEST_CLEANUP_EVENTS"), []byte(strings.Join(e.events, "\n")), 0600)
}

func TestCleanupExplicitMissingFileNeverLoadsPkl(t *testing.T) {
	for _, pklKind := range []string{"missing executable", "marker executable"} {
		for _, mode := range []string{"cleanup", "cleanup-unowned"} {
			for _, command := range []string{"stop", "down"} {
				t.Run(pklKind+"/"+mode+"/"+command, func(t *testing.T) {
					dir := t.TempDir()
					pkl, marker, loaderMarker, events := filepath.Join(dir, "pkl"), filepath.Join(dir, "pkl-called"), filepath.Join(dir, "loader-called"), filepath.Join(dir, "events")
					if pklKind == "marker executable" {
						if err := os.WriteFile(pkl, []byte(fmt.Sprintf("#!/bin/sh\nprintf called > %q\nexit 1\n", marker)), 0700); err != nil {
							t.Fatal(err)
						}
					}
					t.Setenv("DILL_PKL_PATH", pkl)
					t.Setenv("DILL_TEST_LOADER_MARKER", loaderMarker)
					t.Setenv("DILL_TEST_CLEANUP_EVENTS", events)
					out, stderr, code := secretMain(t, mode, true, "-f", filepath.Join(dir, "guaranteed-nonexistent.pkl"), "-p", "demo", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", command)
					wantCode := 0
					if mode == "cleanup-unowned" {
						wantCode = 1
					}
					if code != wantCode {
						t.Fatalf("cleanup exit=%d stdout=%q stderr=%q", code, out, stderr)
					}
					for _, p := range []string{marker, loaderMarker} {
						if _, err := os.Lstat(p); !os.IsNotExist(err) {
							t.Fatal("label-only cleanup invoked config loader/Pkl")
						}
					}
					b, err := os.ReadFile(events)
					if err != nil {
						t.Fatal(err)
					}
					calls := string(b)
					if !strings.Contains(calls, "inspect demo_app") {
						t.Fatal("cleanup skipped private ownership inspection")
					}
					mutated := strings.Contains(calls, "stop immutable-demo_app") && (command == "stop" || strings.Contains(calls, "remove immutable-demo_app"))
					if mode == "cleanup" && !mutated || mode == "cleanup-unowned" && (strings.Contains(calls, "stop ") || strings.Contains(calls, "remove ")) {
						t.Fatalf("cleanup immutable-ID/ownership invariant failed: %s", calls)
					}
				})
			}
		}
	}
}

func TestTrustedPolicyCannotBeSelectedByEnvFile(t *testing.T) {
	t.Setenv("DILL_SECRET_POLICY", "/trusted/policy")
	path := filepath.Join(t.TempDir(), "recipe.env")
	if os.WriteFile(path, []byte("DILL_SECRET_POLICY=/checkout/policy\n"), 0600) != nil {
		t.Fatal("cannot write synthetic env file")
	}
	oldLoad, oldPre := configLoader, secretPreflight
	defer func() { configLoader, secretPreflight = oldLoad, oldPre }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return secretConfig(), nil }
	checked := false
	secretPreflight = func(_ context.Context, r secretfile.Request) (*secretfile.Result, error) {
		checked = true
		if r.PolicyPath != "/trusted/policy" {
			t.Fatal("recipe environment selected policy authority")
		}
		return nil, nil
	}
	_, _, err := captureCommand(t, "", func() error {
		return run([]string{"--env-file", path, "-p", "demo", "--engine", "docker", "--engine-socket", "/run/synthetic.sock", "--secret-target", "lab", "--secret-bindings", "/synthetic/bindings", "validate"})
	})
	if err != nil || !checked {
		t.Fatalf("trusted policy validation failed: %v", err)
	}
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

func TestStrictOrdinaryStackIdentityAndLegacyOverride(t *testing.T) {
	oldLoad, oldEngine := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoad, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	opened := false
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		opened = true
		return newFakeEngine(), nil
	}
	for _, command := range []string{"validate", "plan", "up"} {
		opened = false
		_, _, err := captureCommand(t, "", func() error { return run([]string{"--secret-target", "lab", "-p", "foreign", command}) })
		if err != config.ErrSecretUnavailable || opened {
			t.Fatalf("strict mismatch reached engine: %s %v", command, err)
		}
	}
	_, _, err := captureCommand(t, "", func() error { return run([]string{"-p", "foreign", "validate"}) })
	if err != nil {
		t.Fatalf("legacy override changed: %v", err)
	}
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		cfg := testConfig()
		cfg.Name = ""
		return cfg, nil
	}
	_, _, err = captureCommand(t, "", func() error { return run([]string{"--secret-target", "lab", "-p", "trusted", "validate"}) })
	if err != nil {
		t.Fatalf("empty ordinary name rejected trusted -p: %v", err)
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
		if code != 0 || stderr != "" || !strings.Contains(out, "docker") {
			t.Fatal("static caps lost selector compatibility")
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

func TestSecretStaticDiscoveryDoesNotTreatSelectorValueAsCommand(t *testing.T) {
	for _, args := range [][]string{{"--secret-target", "protocol", "secret-capabilities"}, {"--secret-bindings", "protocol", "caps", "--engine", "docker"}} {
		out, stderr, code := secretMain(t, "static", true, args...)
		if code != 0 || stderr != "" || out == "" {
			t.Fatal("selector value misclassified as protocol command")
		}
	}
}
