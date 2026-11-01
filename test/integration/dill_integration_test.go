//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type planOutput struct {
	Summary struct {
		Create   int `json:"create"`
		Recreate int `json:"recreate"`
		Remove   int `json:"remove"`
		Noop     int `json:"noop"`
	} `json:"summary"`
}

type statusOutput struct {
	Stack      string `json:"stack"`
	Engine     string `json:"engine"`
	Services   int    `json:"services"`
	Containers []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"containers"`
}

type stackOutput struct {
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	Containers int    `json:"containers"`
}

func TestDillRealEngineLifecycle(t *testing.T) {
	env := integrationEnv(t)

	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writeConfig(t, cfg, root, env.engine, stack, "one")
	cleanupStack(t, bin, cfg, stack)

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	runDill(t, true, bin, "-f", cfg, "--timeout", "30s", "wait", "app")
	assertStatus(t, bin, cfg, stack, env.engine, "running")
	assertStacks(t, bin, env.engine, stack)
	assertExecOutput(t, bin, cfg, "one\n")
	assertNoopPlan(t, bin, cfg, 1)
	assertNoopDiff(t, bin, cfg, 1)

	runDill(t, true, bin, "-f", cfg, "stop", "app")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "start", "--no-deps", "app")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "wait", "app")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "restart", "--no-deps", "app")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "wait", "app")

	writeConfig(t, cfg, root, env.engine, stack, "two")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertExecOutput(t, bin, cfg, "two\n")
	assertNoopPlan(t, bin, cfg, 1)

	runDill(t, true, bin, "-f", cfg, "stop", "app")
	runDill(t, true, bin, "-f", cfg, "rm", "--yes", "app")
	assertMissingConfiguredService(t, bin, cfg)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up", "--pull", "missing", "--no-deps", "app")

	runDill(t, true, bin, "-f", cfg, "down")
	assertEmptyStack(t, bin, cfg)
}

func TestDillRealEngineDataPlaneCommands(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-data-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writeDataPlaneConfig(t, cfg, root, env.engine, stack)
	cleanupStack(t, bin, cfg, stack)

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")

	logs := runDill(t, true, bin, "-f", cfg, "logs", "--tail", "2", "app")
	if !strings.Contains(logs, "alpha") || !strings.Contains(logs, "beta") {
		t.Fatalf("logs output missing expected lines: %q", logs)
	}

	port := runDill(t, true, bin, "-f", cfg, "port", "app", "8080")
	if !strings.Contains(port, "8080/tcp") {
		t.Fatalf("port output = %q, want published 8080/tcp", port)
	}
	portJSON := runDill(t, true, bin, "-f", cfg, "port", "--format", "json", "app")
	var ports []string
	if err := json.Unmarshal([]byte(portJSON), &ports); err != nil {
		t.Fatalf("parsing port JSON: %v\n%s", err, portJSON)
	}
	if len(ports) == 0 {
		t.Fatalf("expected published ports, got %s", portJSON)
	}

	top := runDill(t, true, bin, "-f", cfg, "top", "app")
	if !strings.Contains(strings.ToLower(top), "pid") {
		t.Fatalf("top output missing PID header: %q", top)
	}

	hostIn := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(hostIn, []byte("copied\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDill(t, true, bin, "-f", cfg, "cp", hostIn, "app:/tmp/in.txt")
	assertExecOutputCommand(t, bin, cfg, []string{"cat", "/tmp/in.txt"}, "copied\n")

	hostOut := filepath.Join(t.TempDir(), "out.txt")
	runDill(t, true, bin, "-f", cfg, "cp", "app:/tmp/in.txt", hostOut)
	got, err := os.ReadFile(hostOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "copied\n" {
		t.Fatalf("copied file = %q", got)
	}
}

func TestDillRealEngineTeardown(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-teardown-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writeVolumeConfig(t, cfg, root, env.engine, stack)
	cleanupStack(t, bin, cfg, stack)

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	runDill(t, true, bin, "-f", cfg, "teardown", "--yes", "--force")
	assertEmptyStack(t, bin, cfg)
}

func TestDillImageUpdateRecreates(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-image-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	tag := "localhost/dill-it-image:latest"

	// Two distinct images under one tag: moving the tag simulates a registry
	// update without needing a registry.
	runEngine(t, env.engine, "pull", "docker.io/library/busybox:1.36")
	runEngine(t, env.engine, "pull", "docker.io/library/alpine:3.20")
	runEngine(t, env.engine, "tag", "docker.io/library/busybox:1.36", tag)

	// Registered before cleanupStack so it runs after `dill down` (LIFO).
	t.Cleanup(func() {
		_ = exec.Command(engineBinaryName(env.engine), "rmi", "-f", tag).Run()
	})
	writeImageConfig(t, cfg, root, env.engine, stack, tag)
	cleanupStack(t, bin, cfg, stack)

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	before := runEngine(t, env.engine, "inspect", "--format", "{{.Image}}", stack+"_app")
	assertNoopPlan(t, bin, cfg, 1)

	// Point the tag at a different image, as a tag move would.
	runEngine(t, env.engine, "tag", "docker.io/library/alpine:3.20", tag)

	planOut := runDill(t, false, bin, "-f", cfg, "plan", "--format", "json")
	var p planOutput
	if err := json.Unmarshal([]byte(planOut), &p); err != nil {
		t.Fatalf("parsing plan JSON: %v\n%s", err, planOut)
	}
	if p.Summary.Recreate != 1 {
		t.Fatalf("expected 1 recreate after tag move, got %+v\n%s", p.Summary, planOut)
	}
	if !strings.Contains(planOut, `"image_id"`) {
		t.Fatalf("plan did not report an image_id diff:\n%s", planOut)
	}

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	after := runEngine(t, env.engine, "inspect", "--format", "{{.Image}}", stack+"_app")
	if before == after {
		t.Fatalf("container image did not change after tag move (still %s)", before)
	}
}

func TestDillPartialUpKeepsOtherServices(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-partial-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writePartialConfig(t, cfg, root, env.engine, stack, true)
	cleanupStack(t, bin, cfg, stack)

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	if got := containerState(t, env.engine, stack+"_other"); got != "running" {
		t.Fatalf("other state = %q, want running", got)
	}

	// Stop the dependency, then bring up only app without deps. The dependency
	// must stay stopped and app must still start.
	runDill(t, true, bin, "-f", cfg, "stop", "dep")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up", "--no-deps", "app")
	if got := containerState(t, env.engine, stack+"_dep"); got == "running" {
		t.Fatalf("dep was started by --no-deps")
	}
	if got := containerState(t, env.engine, stack+"_app"); got != "running" {
		t.Fatalf("app state = %q, want running", got)
	}

	// A partial up with deps must start dep again and still leave other alone.
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up", "app")
	if got := containerState(t, env.engine, stack+"_dep"); got != "running" {
		t.Fatalf("dep state = %q, want running", got)
	}
	if got := containerState(t, env.engine, stack+"_other"); got != "running" {
		t.Fatalf("other state = %q after partial up, want running", got)
	}

	// A full up still removes services deleted from the config.
	writePartialConfig(t, cfg, root, env.engine, stack, false)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	out := runDill(t, true, bin, "-f", cfg, "ps", "--format", "json")
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parsing ps JSON: %v\n%s", err, out)
	}
	for _, row := range rows {
		if row.Name == stack+"_other" {
			t.Fatalf("other was not removed by full up: %s", out)
		}
	}
}

func TestDillLabelOnlyTeardown(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-labels-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writeConfig(t, cfg, root, env.engine, stack, "one")
	cleanupStack(t, bin, cfg, stack)

	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}

	// Without a config file, -p + --engine must still stop and remove.
	runDill(t, true, bin, "--engine", env.engine, "-p", stack, "stop")
	if got := containerState(t, env.engine, stack+"_app"); got == "running" {
		t.Fatalf("app state = %q after label-only stop", got)
	}

	runDill(t, true, bin, "--engine", env.engine, "-p", stack, "rm", "--yes")
	if names := stackContainerNames(t, env.engine, stack); names != "" {
		t.Fatalf("containers remain after label-only rm: %s", names)
	}

	// No containers left; down should still succeed and clear the network.
	runDill(t, true, bin, "--engine", env.engine, "-p", stack, "down")

	// Recreate and tear down a running stack without the config.
	writeConfig(t, cfg, root, env.engine, stack, "one")
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	runDill(t, true, bin, "--engine", env.engine, "-p", stack, "down")
	if names := stackContainerNames(t, env.engine, stack); names != "" {
		t.Fatalf("containers remain after label-only down: %s", names)
	}
}

func TestDillCommandUXWithoutEngineMutation(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-ux-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writeConfig(t, cfg, root, env.engine, stack, "one")
	runDill(t, true, bin, "-f", cfg, "validate", "--format", "json")
	runDill(t, true, bin, "-f", cfg, "config", "--format", "yaml")
	runDill(t, true, bin, "-f", cfg, "images", "--format", "json")
	runDill(t, true, bin, "-f", cfg, "doctor", "--format", "json")
	runDill(t, true, bin, "-f", cfg, "up", "--dry-run")
	runDill(t, true, bin, "-f", cfg, "init", "--dry-run")

	planOut := runDill(t, false, bin, "-f", cfg, "plan", "--format", "json")
	var p planOutput
	if err := json.Unmarshal([]byte(planOut), &p); err != nil {
		t.Fatalf("parsing changed plan JSON: %v\n%s", err, planOut)
	}
	if p.Summary.Create == 0 {
		t.Fatalf("expected plan to report creates, got %s", planOut)
	}

	diffOut := runDill(t, true, bin, "-f", cfg, "diff", "--format", "json")
	if err := json.Unmarshal([]byte(diffOut), &p); err != nil {
		t.Fatalf("parsing changed diff JSON: %v\n%s", err, diffOut)
	}
	if p.Summary.Create == 0 {
		t.Fatalf("expected diff to report creates, got %s", diffOut)
	}
}

type testEnv struct {
	engine string
}

func integrationEnv(t *testing.T) testEnv {
	t.Helper()
	engine := os.Getenv("DILL_TEST_ENGINE")
	if engine == "" {
		engine = os.Getenv("DILL_ENGINE")
	}
	if engine == "" {
		t.Skip("set DILL_TEST_ENGINE=docker or DILL_TEST_ENGINE=podman")
	}
	if engine != "docker" && engine != "podman" {
		t.Fatalf("unsupported DILL_TEST_ENGINE %q", engine)
	}
	return testEnv{engine: engine}
}

func testTimeout() string {
	if v := os.Getenv("DILL_TEST_TIMEOUT"); v != "" {
		return v
	}
	return "2m"
}

func cleanupStack(t *testing.T, bin, cfg, stack string) {
	t.Helper()
	t.Cleanup(func() {
		if os.Getenv("DILL_TEST_KEEP") == "1" && t.Failed() {
			t.Logf("keeping failed integration stack %s", stack)
			return
		}
		_ = runDill(t, false, bin, "-f", cfg, "down")
	})
}

func writeConfig(t *testing.T, path, root, engine, stack, publicValue string) {
	t.Helper()
	schema := filepath.ToSlash(filepath.Join(root, "dill.pkl"))
	body := fmt.Sprintf(`amends "%s"

config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      pull_policy = "missing"
      environment {
        ["PUBLIC"] = "%s"
      }
      command = List("sh", "-c", "echo ready > /tmp/ready; trap 'exit 0' TERM; while true; do sleep 1; done")
      healthcheck {
        test = "test -f /tmp/ready"
        interval = 1.s
        timeout = 1.s
        retries = 30
      }
    }
  }
}
`, schema, stack, engine, publicValue)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDataPlaneConfig(t *testing.T, path, root, engine, stack string) {
	t.Helper()
	schema := filepath.ToSlash(filepath.Join(root, "dill.pkl"))
	body := fmt.Sprintf(`amends "%s"

config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      pull_policy = "missing"
      ports = List("127.0.0.1:0:8080")
      command = List("sh", "-c", "mkdir -p /www; echo hello > /www/index.html; echo alpha; echo beta; echo ready > /tmp/ready; httpd -f -p 8080 -h /www")
      healthcheck {
        test = "test -f /tmp/ready"
        interval = 1.s
        timeout = 1.s
        retries = 30
      }
    }
  }
}
`, schema, stack, engine)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeImageConfig(t *testing.T, path, root, engine, stack, image string) {
	t.Helper()
	schema := filepath.ToSlash(filepath.Join(root, "dill.pkl"))
	body := fmt.Sprintf(`amends "%s"

config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "%s"
      pull_policy = "never"
      command = List("sleep", "300")
    }
  }
}
`, schema, stack, engine, image)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePartialConfig(t *testing.T, path, root, engine, stack string, includeOther bool) {
	t.Helper()
	schema := filepath.ToSlash(filepath.Join(root, "dill.pkl"))
	other := ""
	if includeOther {
		other = `
    ["other"] {
      image = "docker.io/library/busybox:1.36"
      pull_policy = "missing"
      command = List("sleep", "300")
    }`
	}
	body := fmt.Sprintf(`amends "%s"

config {
  name = "%s"
  engine = "%s"
  services {
    ["dep"] {
      image = "docker.io/library/busybox:1.36"
      pull_policy = "missing"
      command = List("sleep", "300")
    }
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      pull_policy = "missing"
      depends_on = List("dep")
      command = List("sleep", "300")
    }%s
  }
}
`, schema, stack, engine, other)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeVolumeConfig(t *testing.T, path, root, engine, stack string) {
	t.Helper()
	schema := filepath.ToSlash(filepath.Join(root, "dill.pkl"))
	volume := stack + "-data"
	body := fmt.Sprintf(`amends "%s"

config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      pull_policy = "missing"
      volumes = List("%s:/data")
      command = List("sh", "-c", "echo ready > /tmp/ready; trap 'exit 0' TERM; while true; do sleep 1; done")
      healthcheck {
        test = "test -f /tmp/ready"
        interval = 1.s
        timeout = 1.s
        retries = 30
      }
    }
  }
}
`, schema, stack, engine, volume)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertExecOutput(t *testing.T, bin, cfg, want string) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "exec", "app", "sh", "-c", "printf '%s\\n' \"$PUBLIC\"")
	if out != want {
		t.Fatalf("exec output = %q, want %q", out, want)
	}
}

func assertExecOutputCommand(t *testing.T, bin, cfg string, command []string, want string) {
	t.Helper()
	args := append([]string{"-f", cfg, "exec", "app"}, command...)
	out := runDill(t, true, bin, args...)
	if out != want {
		t.Fatalf("exec output = %q, want %q", out, want)
	}
}

func assertNoopPlan(t *testing.T, bin, cfg string, wantNoop int) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "plan", "--format", "json")
	var p planOutput
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("parsing plan JSON: %v\n%s", err, out)
	}
	if p.Summary.Create != 0 || p.Summary.Recreate != 0 || p.Summary.Remove != 0 || p.Summary.Noop != wantNoop {
		t.Fatalf("unexpected plan summary: %+v\n%s", p.Summary, out)
	}
}

func assertNoopDiff(t *testing.T, bin, cfg string, wantNoop int) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "diff", "--format", "json")
	var p planOutput
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("parsing diff JSON: %v\n%s", err, out)
	}
	if p.Summary.Noop != wantNoop {
		t.Fatalf("unexpected diff summary: %+v\n%s", p.Summary, out)
	}
}

func assertStatus(t *testing.T, bin, cfg, stack, engine, wantState string) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "status", "--format", "json")
	var status statusOutput
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("parsing status JSON: %v\n%s", err, out)
	}
	if status.Stack != stack || status.Engine != engine || status.Services != 1 {
		t.Fatalf("unexpected status header: %+v\n%s", status, out)
	}
	if len(status.Containers) != 1 || status.Containers[0].State != wantState {
		t.Fatalf("unexpected status containers: %+v\n%s", status.Containers, out)
	}
}

func assertStacks(t *testing.T, bin, engine, stack string) {
	t.Helper()
	out := runDill(t, true, bin, "--engine", engine, "stacks", "--format", "json")
	var stacks []stackOutput
	if err := json.Unmarshal([]byte(out), &stacks); err != nil {
		t.Fatalf("parsing stacks JSON: %v\n%s", err, out)
	}
	for _, s := range stacks {
		if s.Name == stack && s.Engine == engine && s.Containers > 0 {
			return
		}
	}
	t.Fatalf("stack %q not found in %s", stack, out)
}

func assertMissingConfiguredService(t *testing.T, bin, cfg string) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "ps", "--all", "--format", "json")
	var rows []struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parsing ps --all JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].State != "missing" {
		t.Fatalf("expected one missing configured service, got %s", out)
	}
}

func assertEmptyStack(t *testing.T, bin, cfg string) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "ps", "--format", "json")
	var rows []any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parsing ps JSON: %v\n%s", err, out)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty stack after down, got %s", out)
	}
}

func engineBinaryName(engine string) string {
	if engine == "podman" {
		return "podman"
	}
	return "docker"
}

func stackContainerNames(t *testing.T, engine, stack string) string {
	t.Helper()
	return strings.TrimSpace(runEngine(t, engine, "ps", "-a", "--filter", "label=dill.stack="+stack, "--format", "{{.Names}}"))
}

func containerState(t *testing.T, engine, name string) string {
	t.Helper()
	return runEngine(t, engine, "inspect", "--format", "{{.State.Status}}", name)
}

func runEngine(t *testing.T, engine string, args ...string) string {
	t.Helper()
	bin := engineBinaryName(engine)
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", bin, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func dillBinary(t *testing.T, root string) string {
	t.Helper()
	if bin := os.Getenv("DILL_BINARY"); bin != "" {
		return bin
	}
	bin := filepath.Join(t.TempDir(), "dill")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-tags", "containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs", "-o", bin, "./cmd/dill")
	cmd.Dir = root
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building dill: %v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func runDill(t *testing.T, requireSuccess bool, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if requireSuccess && err != nil {
		t.Fatalf("%s %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			bin, strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}
