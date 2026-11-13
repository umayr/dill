//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
)

const alphaPreparedImage = "docker.io/library/busybox:1.36"

// Readiness/barrier fixtures must not self-expire or spend the stop deadline
// waiting for PID 1's default signal handling. The test owns explicit cleanup.
const alphaPreparedCommand = "trap 'exit 0' TERM; while true; do sleep 1; done"

func alphaGuardrailEnvironment(t *testing.T) testEnv {
	t.Helper()
	if os.Getenv("DILL_TEST_ALPHA_GUARDRAILS") != "1" {
		t.Skip("requires disposable guardrail qualification opt-in")
	}
	env := integrationEnv(t)
	if !strings.HasPrefix(os.Getenv("DILL_ENGINE_SOCKET"), "unix:///") {
		t.Fatal("guardrail qualification requires explicit pinned local socket")
	}
	return env
}

func writeAlphaPreparedConfig(t *testing.T, cfg, root, engine, stack, image, extra string, other bool, declarations ...string) {
	t.Helper()
	omitted := ""
	if other {
		omitted = fmt.Sprintf(`["other"] { image = %q; pullPolicy = "never"; command = List("sh", "-c", %q) }`, image, alphaPreparedCommand)
	}
	body := fmt.Sprintf(`amends %q
config {
  name = %q
  engine = %q
  %s
  services {
    ["app"] {
      image = %q
      pullPolicy = "never"
      command = List("sh", "-c", %q)
      %s
    }
    %s
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, engine, strings.Join(declarations, "\n"), image, alphaPreparedCommand, extra, omitted)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func evaluateAlphaFixture(t *testing.T, cfg string) {
	t.Helper()
	pkl := os.Getenv("DILL_PKL_PATH")
	if pkl == "" {
		pkl = "pkl"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, pkl, "eval", cfg).Output()
	if err != nil {
		t.Fatal("synthetic profile fixture failed Pkl evaluation; policy rejection was not exercised")
	}
	var document struct {
		Config *config.DillConfig `json:"config"`
	}
	if json.Unmarshal(data, &document) != nil || config.ValidateRuntime(document.Config) != nil {
		t.Fatal("synthetic profile fixture failed ordinary runtime validation; policy rejection was not exercised")
	}
}

func assertAlphaSecretBoundary(t *testing.T, result alphaOperationResult) {
	t.Helper()
	if result.Outcome != "failed" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "SECRET_UNAVAILABLE" || result.Errors[0].Message != "Secret configuration or source unavailable." {
		t.Fatal("valid synthetic secret request did not reach the deliberate secret-unavailable boundary")
	}
}

func TestAlphaGuardrailFixtureEvaluation(t *testing.T) {
	root := repoRoot(t)
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	for _, extra := range []string{
		`volumes = List("data:/data")`,
		`volumes = List(new Mount { type = "tmpfs"; target = "/data" })`,
		`volumes = List(new Mount { source = "synthetic-external"; target = "/data"; externalVolume = true })`,
		`volumes = List(new Mount { source = "data"; target = "/data"; name = "synthetic-explicit" })`,
		`secrets { new { name = "synthetic"; target = "/run/secrets/synthetic" } }`,
	} {
		declarations := ""
		if strings.HasPrefix(extra, "secrets {") {
			declarations = `secrets { new { name = "synthetic" } }`
		}
		writeAlphaPreparedConfig(t, cfg, root, "docker", "synthetic-offline", alphaPreparedImage, extra, false, declarations)
		evaluateAlphaFixture(t, cfg)
	}
	// Independently author a valid secret request, not an evaluator failure.
	writeAlphaPreparedConfig(t, cfg, root, "docker", "synthetic-offline", alphaPreparedImage, `secrets { new { name = "synthetic"; target = "/run/secrets/synthetic" } }`, false, `secrets { new { name = "synthetic" } }`)
	evaluateAlphaFixture(t, cfg)
	// A regular file is deliberately NOT an engine socket: no live engine needed.
	socket := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, dillBinary(t, root), "operation", "up", "--contract", "dill.operation/1", "--engine", "docker", "--engine-socket", "unix://"+filepath.ToSlash(socket), "-p", "synthetic-offline", "-f", cfg, "--alpha-disposable")
	data, err := cmd.Output()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 1 {
		t.Fatal("synthetic secret boundary returned wrong process outcome")
	}
	result, err := decodeAlphaOperation(data)
	if err != nil {
		t.Fatal(err)
	}
	assertAlphaSecretBoundary(t, result)
}

// Selected stable inspect fields avoid moving healthcheck timestamps. All raw
// inspect data stays in memory in this exclusively synthetic fixture.
func alphaResourceSnapshot(t *testing.T, engine string) string {
	t.Helper()
	rows := []string{}
	for _, resource := range []struct {
		kind, list string
	}{
		{"container", "ps"}, {"network", "network"}, {"volume", "volume"}, {"image", "images"},
	} {
		var output string
		switch resource.kind {
		case "container":
			output = runEngine(t, engine, "ps", "-aq", "--no-trunc")
		case "image":
			output = runEngine(t, engine, "images", "-q", "--no-trunc")
		default:
			output = runEngine(t, engine, resource.list, "ls", "-q")
		}
		ids := strings.Fields(output)
		sort.Strings(ids)
		for _, id := range ids {
			args := []string{resource.kind, "inspect", id}
			if resource.kind == "container" {
				args = []string{"inspect", "--format", "{{.Id}}|{{.State.Status}}|{{json .Config.Labels}}|{{json .Mounts}}", id}
			}
			rows = append(rows, resource.kind+"|"+id+"|"+runEngine(t, engine, args...))
		}
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

func TestAlphaNoPruneBothEngines(t *testing.T) {
	env := alphaGuardrailEnvironment(t)
	root, bin := repoRoot(t), dillBinary(t, repoRoot(t))
	stack := fmt.Sprintf("dill-alpha-no-prune-%s-%d", env.engine, time.Now().UnixNano())
	t.Cleanup(func() { alphaVerifyScopedCleanup(t, env.engine, stack) })
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	// Preparation is separate from the pull-never operations under test.
	runEngine(t, env.engine, "pull", alphaPreparedImage)
	writeAlphaPreparedConfig(t, cfg, root, env.engine, stack, alphaPreparedImage, "", true)
	cleanupStack(t, bin, cfg, stack)
	// Deliberately isolated legacy CLI: NO coordination or alpha-disposable flags.
	// Qualified mode forbids orphans; its rejection cannot stand in for this
	// actual successful omitted-service preservation case.
	runDill(t, true, bin, "--engine", env.engine, "--engine-socket", os.Getenv("DILL_ENGINE_SOCKET"), "-p", stack, "-f", cfg, "up", "--no-prune")
	before := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}|{{.State.Status}}", stack+"_other")
	if !strings.HasSuffix(before, "|running") {
		t.Fatal("no-prune preparation did not establish a running omitted service")
	}
	writeAlphaPreparedConfig(t, cfg, root, env.engine, stack, alphaPreparedImage, "", false)
	runDill(t, true, bin, "--engine", env.engine, "--engine-socket", os.Getenv("DILL_ENGINE_SOCKET"), "-p", stack, "-f", cfg, "up", "--no-prune")
	if after := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}|{{.State.Status}}", stack+"_other"); after != before {
		t.Fatal("explicit no-prune changed omitted service identity/state")
	}
}

func TestAlphaDisposableProfileDocker(t *testing.T) {
	env := alphaGuardrailEnvironment(t)
	if env.engine != "docker" {
		t.Fatal("Docker-only profile test selected for unsupported adapter")
	}
	root, bin := repoRoot(t), dillBinary(t, repoRoot(t))
	stack := fmt.Sprintf("dill-alpha-profile-%d", time.Now().UnixNano())
	t.Cleanup(func() { alphaVerifyScopedCleanup(t, env.engine, stack) })
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	runEngine(t, env.engine, "pull", alphaPreparedImage)
	var metadata []struct {
		ID     string `json:"Id"`
		Config *struct {
			Volumes map[string]any `json:"Volumes"`
		} `json:"Config"`
	}
	if err := json.Unmarshal([]byte(runEngine(t, env.engine, "image", "inspect", alphaPreparedImage)), &metadata); err != nil || len(metadata) != 1 || metadata[0].Config == nil || metadata[0].ID == "" || len(metadata[0].Config.Volumes) != 0 {
		t.Fatal("positive prepared fixture lacks verified immutable image ID or declares VOLUME")
	}
	writeAlphaPreparedConfig(t, cfg, root, env.engine, stack, alphaPreparedImage, `volumes = List("data:/data")`, true)
	// Volumes are deliberately retained by Dill; test-owned removal is separate.
	t.Cleanup(func() { alphaCleanupResource(t, env.engine, "volume", stack+"_data") })
	cleanupStack(t, bin, cfg, stack)
	runProfile := func(t *testing.T, wantExit int, options ...string) alphaOperationResult {
		t.Helper()
		args := []string{"operation", "up", "--contract", "dill.operation/1", "--engine", env.engine, "--engine-socket", os.Getenv("DILL_ENGINE_SOCKET"), "-p", stack, "-f", cfg, "--alpha-disposable", "--no-prune=false"}
		args = append(args, options...)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		data, err := cmd.Output()
		code := 0
		if err != nil {
			if exited, ok := err.(*exec.ExitError); ok {
				code = exited.ExitCode()
			} else {
				t.Fatalf("profile command did not return terminal result: %T", err)
			}
		}
		result, decodeErr := decodeAlphaOperation(data)
		if decodeErr != nil || code != wantExit {
			t.Fatalf("profile result invalid or wrong exit: decode=%v exit=%d want=%d (raw output withheld)", decodeErr, code, wantExit)
		}
		for _, resource := range result.Resources {
			if resource.Kind == "image" && resource.Action == "pull" {
				t.Fatal("prepared-image operation attempted a pull")
			}
		}
		if wantExit == 0 && (result.Outcome != "succeeded" || result.Effects != "complete") {
			t.Fatal("prepared profile did not complete")
		}
		if wantExit != 0 && result.Effects != "none" {
			t.Fatal("rejected profile attempted an engine mutation")
		}
		return result
	}
	runProfile(t, 0)
	if image := runEngine(t, env.engine, "inspect", "--format", "{{.Image}}", stack+"_app"); image != metadata[0].ID {
		t.Fatal("profile create content differs from inspected immutable image ID")
	}
	if reference := runEngine(t, env.engine, "inspect", "--format", "{{.Config.Image}}", stack+"_app"); reference != metadata[0].ID {
		t.Fatal("profile create reference was not pinned to inspected immutable image ID")
	}
	appBefore := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app")
	repeatSince := time.Now().UTC().Format(time.RFC3339Nano)
	runProfile(t, 0)
	repeatUntil := time.Now().UTC().Format(time.RFC3339Nano)
	if after := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app"); after != appBefore {
		t.Fatal("repeat prepared-image profile replaced a converged container")
	}
	if events := runEngine(t, env.engine, "events", "--since", repeatSince, "--until", repeatUntil, "--format", "{{json .}}"); events != "" {
		t.Fatal("repeat prepared-image profile mutated the engine")
	}
	// Config.Image's immutable create ID must still reconcile with its desired
	// reference when both identify the same content; moving tags are not no-ops.
	planCtx, planCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer planCancel()
	planCmd := exec.CommandContext(planCtx, bin, "operation", "plan", "--contract", "dill.operation/1", "--engine", env.engine, "--engine-socket", os.Getenv("DILL_ENGINE_SOCKET"), "-p", stack, "-f", cfg)
	planData, planErr := planCmd.Output()
	planResult, decodeErr := decodeAlphaOperation(planData)
	if planErr != nil || decodeErr != nil || planResult.Outcome != "succeeded" || planResult.Effects != "none" || planResult.ChangesDetected {
		t.Fatal("prepared immutable create does not produce a same-content no-op plan")
	}
	runEngine(t, env.engine, "exec", stack+"_app", "sh", "-c", "printf synthetic-retained-v1 > /data/value")
	omitted := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}|{{.State.Status}}", stack+"_other")
	writeAlphaPreparedConfig(t, cfg, root, env.engine, stack, alphaPreparedImage, `volumes = List("data:/data")`, false)
	runProfile(t, 0)
	if after := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app"); after != appBefore {
		t.Fatal("omitting another service recreated the unchanged desired container")
	}
	if after := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}|{{.State.Status}}", stack+"_other"); after != omitted {
		t.Fatal("profile failed implied no-prune with explicit no-prune=false")
	}

	// Dedicated synthetic foreign resources prove more than just name collision.
	foreignVolume, foreignNetwork := stack+"-foreign-data", stack+"-foreign-net"
	runEngine(t, env.engine, "volume", "create", foreignVolume)
	runEngine(t, env.engine, "network", "create", foreignNetwork)
	foreign := runEngine(t, env.engine, "run", "-d", "--name", stack+"-foreign", "--network", foreignNetwork, "-v", foreignVolume+":/foreign", alphaPreparedImage, "sleep", "300")
	runEngine(t, env.engine, "exec", foreign, "sh", "-c", "printf synthetic-foreign-v1 > /foreign/value")
	t.Cleanup(func() {
		alphaCleanupResource(t, env.engine, "container", foreign)
		alphaCleanupResource(t, env.engine, "network", foreignNetwork)
		alphaCleanupResource(t, env.engine, "volume", foreignVolume)
	})
	// Prepare a local metadata-only negative image; no image VOLUME workload runs.
	seed := runEngine(t, env.engine, "create", alphaPreparedImage, "true")
	volumeImage := "localhost/" + stack + ":declared-volume"
	runEngine(t, env.engine, "commit", "--change", `VOLUME ["/hidden"]`, seed, volumeImage)
	runEngine(t, env.engine, "rm", seed)
	t.Cleanup(func() { alphaCleanupResource(t, env.engine, "image", volumeImage) })

	for _, tc := range []struct {
		name, image, extra string
		options            []string
	}{
		{"bind", alphaPreparedImage, `volumes = List("/synthetic-missing-bind:/data")`, nil},
		{"capability", alphaPreparedImage, `capAdd = List("NET_ADMIN")`, nil},
		{"host-network", alphaPreparedImage, `networkMode = "host"`, nil},
		{"device", alphaPreparedImage, `devices = List("/synthetic-device:/dev/synthetic")`, nil},
		{"anonymous-volume", alphaPreparedImage, `volumes = List("/data")`, nil},
		{"tmpfs", alphaPreparedImage, `volumes = List(new Mount { type = "tmpfs"; target = "/data" })`, nil},
		{"secret-grant", alphaPreparedImage, `secrets { new { name = "synthetic"; target = "/run/secrets/synthetic" } }`, nil},
		{"external-volume", alphaPreparedImage, fmt.Sprintf(`volumes = List(new Mount { source = %q; target = "/data"; externalVolume = true })`, foreignVolume), nil},
		{"explicit-volume-name", alphaPreparedImage, fmt.Sprintf(`volumes = List(new Mount { source = "data"; target = "/data"; name = %q })`, foreignVolume), nil},
		{"pull-always", alphaPreparedImage, `volumes = List("data:/data")`, []string{"--pull", "always"}},
		{"unprepared-image", "localhost/" + stack + ":never-prepared", `volumes = List("data:/data")`, nil},
		{"image-volume", volumeImage, "", nil},
		{"covered-image-volume", volumeImage, `volumes = List("data:/hidden")`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := alphaResourceSnapshot(t, env.engine)
			declarations := ""
			if tc.name == "secret-grant" {
				declarations = `secrets { new { name = "synthetic" } }`
			}
			writeAlphaPreparedConfig(t, cfg, root, env.engine, stack, tc.image, tc.extra, false, declarations)
			evaluateAlphaFixture(t, cfg)
			since := time.Now().UTC().Format(time.RFC3339Nano)
			result := runProfile(t, 1, tc.options...)
			if tc.name == "secret-grant" {
				assertAlphaSecretBoundary(t, result)
			}
			until := time.Now().UTC().Format(time.RFC3339Nano)
			if events := runEngine(t, env.engine, "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" {
				t.Fatal("rejected profile produced engine events; zero-write assertion failed")
			}
			if after := alphaResourceSnapshot(t, env.engine); after != before {
				t.Fatal("unsupported profile changed container/network/volume/image inventory or state")
			}
			if marker := runEngine(t, env.engine, "exec", foreign, "cat", "/foreign/value"); marker != "synthetic-foreign-v1" {
				t.Fatal("unsupported profile changed foreign data")
			}
		})
	}
	for _, kind := range []string{"volume", "network", "container"} {
		t.Run("foreign-"+kind+"-collision", func(t *testing.T) {
			collision := stack + "-collision-" + kind
			var identity string
			switch kind {
			case "volume":
				identity = runEngine(t, env.engine, "volume", "create", collision+"_data")
			case "network":
				identity = runEngine(t, env.engine, "network", "create", collision)
			case "container":
				identity = runEngine(t, env.engine, "run", "-d", "--name", collision+"_app", alphaPreparedImage, "sleep", "300")
			}
			t.Cleanup(func() { alphaCleanupResource(t, env.engine, kind, identity) })
			writeAlphaPreparedConfig(t, cfg, root, env.engine, collision, alphaPreparedImage, `volumes = List("data:/data")`, false)
			before := alphaResourceSnapshot(t, env.engine)
			since := time.Now().UTC().Format(time.RFC3339Nano)
			runProfile(t, 1, "-p", collision)
			until := time.Now().UTC().Format(time.RFC3339Nano)
			if events := runEngine(t, env.engine, "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" {
				t.Fatal("foreign collision rejection produced engine events")
			}
			if after := alphaResourceSnapshot(t, env.engine); after != before {
				t.Fatal("foreign collision changed resource identities/state")
			}
		})
	}
	writeAlphaPreparedConfig(t, cfg, root, env.engine, stack, alphaPreparedImage, `volumes = List("data:/data")`, false)
	runProfile(t, 0, "--pull", "never")
	if marker := runEngine(t, env.engine, "exec", stack+"_app", "cat", "/data/value"); marker != "synthetic-retained-v1" {
		t.Fatal("profile retry lost retained synthetic volume data")
	}
	// Explicit ordinary down is non-prompting and must retain dedicated storage.
	runDillDown(t, env.engine, stack, bin, "--engine", env.engine, "--engine-socket", os.Getenv("DILL_ENGINE_SOCKET"), "-p", stack, "-f", cfg, "down")
	if !volumeExists(t, env.engine, stack+"_data") {
		t.Fatal("down deleted retained profile volume")
	}
	if state := containerState(t, env.engine, foreign); state != "running" {
		t.Fatal("profile cleanup changed synthetic foreign container")
	}
	t.Run("captured-name-reuse", func(t *testing.T) {
		alphaRemovalNameReuse(t, env.engine, bin, root, stack+"-intent")
	})
}

type alphaPromptBarrier struct {
	text  strings.Builder
	once  sync.Once
	ready chan struct{}
}

func (p *alphaPromptBarrier) Write(data []byte) (int, error) {
	_, _ = p.text.Write(data)
	if strings.Contains(p.text.String(), "Continue?") {
		p.once.Do(func() { close(p.ready) })
	}
	return len(data), nil
}

func alphaRemovalNameReuse(t *testing.T, engine, bin, root, stack string) {
	t.Helper()
	t.Cleanup(func() { alphaVerifyScopedCleanup(t, engine, stack) })
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	socket := os.Getenv("DILL_ENGINE_SOCKET")
	writeAlphaPreparedConfig(t, cfg, root, engine, stack, alphaPreparedImage, "", false)
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "--engine", engine, "--engine-socket", socket, "-p", stack, "-f", cfg, "up", "--alpha-disposable")
	runDill(t, true, bin, "--engine", engine, "--engine-socket", socket, "-p", stack, "-f", cfg, "stop")
	original := runEngine(t, engine, "inspect", "--format", "{{.Id}}", stack+"_app")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "operation", "rm", "--contract", "dill.operation/1", "--engine", engine, "--engine-socket", socket, "-p", stack, "-f", cfg)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var stdout bytes.Buffer
	prompt := &alphaPromptBarrier{ready: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = &stdout, prompt
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-done
		}
	})
	select {
	case <-prompt.ready:
	case <-done:
		finished = true
		t.Fatal("removal ended before captured-intent confirmation")
	case <-ctx.Done():
		t.Fatal("removal never reached captured-intent confirmation")
	}
	// Deliberate fixture replacement occurs AFTER capture and BEFORE consent.
	runEngine(t, engine, "rm", original)
	foreign := runEngine(t, engine, "run", "-d", "--name", stack+"_app", alphaPreparedImage, "sleep", "300")
	t.Cleanup(func() { alphaCleanupResource(t, engine, "container", foreign) })
	before := alphaResourceSnapshot(t, engine)
	since := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := input.Write([]byte("yes\n")); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	waitErr := <-done
	finished = true
	until := time.Now().UTC().Format(time.RFC3339Nano)
	result, decodeErr := decodeAlphaOperation(stdout.Bytes())
	var exited *exec.ExitError
	if !errors.As(waitErr, &exited) || exited.ExitCode() != 1 || decodeErr != nil || result.Outcome != "failed" || result.Effects != "none" {
		t.Fatal("same-name replacement was not rejected with exact exit 1 and no effects")
	}
	if len(result.Errors) != 1 || result.Errors[0].Code != "OPERATION_FAILED" || result.Errors[0].Message != "Operation failed. Inspect the target before retrying if effects are uncertain." {
		t.Fatal("same-name replacement did not preserve the fixed failure contract")
	}
	captured := false
	for _, resource := range result.Resources {
		if resource.Kind == "container" && resource.Action == "intent.removeStoppedCandidate" && resource.IdentityDigest == alphaDigest("dill.resource/1\x00container\x00"+original) && resource.Effect == "none" {
			captured = true
		}
	}
	if !captured || strings.Contains(prompt.text.String(), original) {
		t.Fatal("confirmation did not preserve opaque original immutable intent")
	}
	if events := runEngine(t, engine, "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" {
		t.Fatal("stale removal consent produced engine mutations")
	}
	if after := alphaResourceSnapshot(t, engine); after != before || containerState(t, engine, foreign) != "running" {
		t.Fatal("stale removal consent changed the replacement resource")
	}
}
