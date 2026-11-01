package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestValidateFormatRejectsUnknownValue(t *testing.T) {
	err := validateFormat("xml", "text", "json")
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("validateFormat returned %T, want usageError", err)
	}
}

func TestSelectServicesRejectsUnknownService(t *testing.T) {
	services := map[string]*config.Service{"web": {Image: "nginx"}}
	_, err := selectServices(services, []string{"missing"}, false)
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("selectServices returned %T, want usageError", err)
	}
}

func TestSelectServicesIncludesDependencies(t *testing.T) {
	services := map[string]*config.Service{
		"web": {Image: "nginx", DependsOn: []string{"db"}},
		"db":  {Image: "postgres"},
	}
	got, err := selectServices(services, []string{"web"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["web"]; !ok {
		t.Fatal("selected services missing web")
	}
	if _, ok := got["db"]; !ok {
		t.Fatal("selected services missing dependency db")
	}
}

func TestRunPlanRejectsUnknownFormatBeforeLoadingConfig(t *testing.T) {
	opts := commandOptions{ConfigFile: "does-not-exist.pkl"}
	err := runPlan(context.Background(), opts, []string{"--format", "xml"})
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("runPlan returned %T, want usageError", err)
	}
}

func TestRunImagesRejectsUnexpectedArgs(t *testing.T) {
	opts := commandOptions{ConfigFile: "does-not-exist.pkl"}
	err := runImages(context.Background(), opts, []string{"web"})
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("runImages returned %T, want usageError", err)
	}
}

func TestDiffReturnsSuccessWhenChangesExist(t *testing.T) {
	fake := newFakeEngine()
	withTestRuntime(t, fake, testConfig(), func() {
		_, _, err := captureCommand(t, "", func() error {
			return run([]string{"diff", "--format", "json"})
		})
		if err != nil {
			t.Fatalf("diff returned error: %v", err)
		}
	})
}

func TestRmPromptsAndAbortsByDefault(t *testing.T) {
	fake := newFakeEngine()
	fake.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
	withTestRuntime(t, fake, testConfig(), func() {
		_, stderr, err := captureCommand(t, "n\n", func() error {
			return runRm(context.Background(), commandOptions{ConfigFile: "compose.pkl"}, nil)
		})
		if err != nil {
			t.Fatalf("rm returned error: %v", err)
		}
		if len(fake.removed) != 0 {
			t.Fatalf("rm removed containers after negative confirmation: %v", fake.removed)
		}
		if !strings.Contains(stderr, "Continue?") || !strings.Contains(stderr, "aborted") {
			t.Fatalf("rm prompt stderr = %q", stderr)
		}
	})
}

func TestRmYesSkipsPrompt(t *testing.T) {
	fake := newFakeEngine()
	fake.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
	withTestRuntime(t, fake, testConfig(), func() {
		_, stderr, err := captureCommand(t, "", func() error {
			return runRm(context.Background(), commandOptions{ConfigFile: "compose.pkl", Yes: true}, nil)
		})
		if err != nil {
			t.Fatalf("rm returned error: %v", err)
		}
		if len(fake.removed) != 1 || fake.removed[0] != "app_web" {
			t.Fatalf("rm removed = %v, want app_web", fake.removed)
		}
		if strings.Contains(stderr, "Continue?") {
			t.Fatalf("rm prompted despite --yes: %q", stderr)
		}
	})
}

func TestTeardownPromptsAndAbortsByDefault(t *testing.T) {
	fake := newFakeEngine()
	fake.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
	withTestRuntime(t, fake, testConfig(), func() {
		_, stderr, err := captureCommand(t, "no\n", func() error {
			return runTeardown(context.Background(), commandOptions{ConfigFile: "compose.pkl"}, nil)
		})
		if err != nil {
			t.Fatalf("teardown returned error: %v", err)
		}
		if len(fake.removed) != 0 || fake.networkRemoved {
			t.Fatalf("teardown mutated after negative confirmation: removed=%v network=%v", fake.removed, fake.networkRemoved)
		}
		if !strings.Contains(stderr, "Continue?") || !strings.Contains(stderr, "aborted") {
			t.Fatalf("teardown prompt stderr = %q", stderr)
		}
	})
}

func TestInitDryRunDoesNotWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/compose.pkl"
	stdout, _, err := captureCommand(t, "", func() error {
		return runInit(commandOptions{ConfigFile: path, DryRun: true}, nil)
	})
	if err != nil {
		t.Fatalf("init --dry-run returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("init --dry-run created file or stat failed: %v", err)
	}
	if !strings.Contains(stdout, "would create") {
		t.Fatalf("init --dry-run stdout = %q", stdout)
	}
}

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"usage", usageError{"unknown command"}, 2},
		{"wrapped usage", fmt.Errorf("wrap: %w", usageError{"bad flag"}), 2},
		{"plan drift", errPlanHasChanges, 1},
		{"doctor issues", errDoctorFoundIssues, 1},
		{"operational", errors.New("boom"), 1},
	}
	for _, c := range cases {
		if got := exitCodeFor(c.err); got != c.want {
			t.Errorf("%s: exitCodeFor = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestLogsFIsConfigFileNotFollow(t *testing.T) {
	fake := newFakeEngine()
	var loaded string
	oldEngineFactory := engineFactory
	oldConfigLoader := configLoader
	engineFactory = func(context.Context, string) (orchestrator.Engine, error) { return fake, nil }
	configLoader = func(_ context.Context, path string) (*config.DillConfig, error) {
		loaded = path
		return testConfig(), nil
	}
	t.Cleanup(func() {
		engineFactory = oldEngineFactory
		configLoader = oldConfigLoader
	})

	if _, _, err := captureCommand(t, "", func() error {
		return run([]string{"logs", "-f", "custom.pkl"})
	}); err != nil {
		t.Fatalf("logs -f returned error: %v", err)
	}
	if loaded != "custom.pkl" {
		t.Fatalf("config loader received %q, want %q", loaded, "custom.pkl")
	}

	// --follow must parse as a bool flag without consuming the next token.
	if _, _, err := captureCommand(t, "", func() error {
		return run([]string{"logs", "--follow"})
	}); err != nil {
		t.Fatalf("logs --follow returned error: %v", err)
	}
}

func testConfig() *config.DillConfig {
	return &config.DillConfig{
		Name:   "app",
		Engine: "docker",
		Services: map[string]*config.Service{
			"web": {Image: "busybox"},
		},
	}
}

func withTestRuntime(t *testing.T, engine orchestrator.Engine, cfg *config.DillConfig, fn func()) {
	t.Helper()
	oldEngineFactory := engineFactory
	oldConfigLoader := configLoader
	engineFactory = func(context.Context, string) (orchestrator.Engine, error) {
		return engine, nil
	}
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		return cfg, nil
	}
	t.Cleanup(func() {
		engineFactory = oldEngineFactory
		configLoader = oldConfigLoader
	})
	fn()
}

func captureCommand(t *testing.T, stdin string, fn func() error) (string, string, error) {
	t.Helper()
	oldStdout, oldStderr, oldStdin := os.Stdout, os.Stderr, os.Stdin
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(inW, stdin); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	os.Stdout, os.Stderr, os.Stdin = outW, errW, inR
	runErr := fn()
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr, os.Stdin = oldStdout, oldStderr, oldStdin
	var stdout, stderr bytes.Buffer
	if _, err := io.Copy(&stdout, outR); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&stderr, errR); err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), runErr
}

type fakeEngine struct {
	statuses       map[string]*orchestrator.ContainerStatus
	removed        []string
	pulled         []string
	networkRemoved bool
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{statuses: make(map[string]*orchestrator.ContainerStatus)}
}

func (f *fakeEngine) PullImage(_ context.Context, image string, _ io.Writer) error {
	f.pulled = append(f.pulled, image)
	return nil
}
func (f *fakeEngine) ImageExists(context.Context, string) (bool, error)    { return false, nil }
func (f *fakeEngine) LocalImageID(context.Context, string) (string, error) { return "", nil }
func (f *fakeEngine) EnsureVolume(context.Context, string, string) error   { return nil }
func (f *fakeEngine) ListStackVolumes(context.Context, string) ([]string, error) {
	return nil, nil
}
func (f *fakeEngine) StartService(context.Context, string, *config.Service, string) (string, error) {
	return "id", nil
}
func (f *fakeEngine) StopService(context.Context, string) error { return nil }
func (f *fakeEngine) StartExisting(context.Context, string) error {
	return nil
}
func (f *fakeEngine) RemoveService(_ context.Context, name string) error {
	f.removed = append(f.removed, name)
	delete(f.statuses, name)
	return nil
}
func (f *fakeEngine) IsReady(context.Context, string, bool) (bool, error) { return true, nil }
func (f *fakeEngine) ServiceStatus(_ context.Context, name string) (*orchestrator.ContainerStatus, error) {
	cs, ok := f.statuses[name]
	if !ok {
		return nil, orchestrator.ErrNotFound
	}
	return cs, nil
}
func (f *fakeEngine) Logs(context.Context, string, orchestrator.LogOptions, io.Writer) error {
	return nil
}
func (f *fakeEngine) InspectConfig(context.Context, string) (*orchestrator.LiveConfig, error) {
	return nil, orchestrator.ErrNotFound
}
func (f *fakeEngine) ListStack(context.Context, string) ([]string, error) {
	names := make([]string, 0, len(f.statuses))
	for name := range f.statuses {
		names = append(names, name)
	}
	return names, nil
}
func (f *fakeEngine) ListStacks(context.Context) ([]orchestrator.StackStatus, error) {
	return []orchestrator.StackStatus{{Name: "app", Engine: "docker", Containers: len(f.statuses)}}, nil
}
func (f *fakeEngine) EnsureNetwork(context.Context, string) error { return nil }
func (f *fakeEngine) RemoveNetwork(context.Context, string) error {
	f.networkRemoved = true
	return nil
}
func (f *fakeEngine) RemoveImage(context.Context, string, bool) error  { return nil }
func (f *fakeEngine) RemoveVolume(context.Context, string, bool) error { return nil }
func (f *fakeEngine) Close() error                                     { return nil }
