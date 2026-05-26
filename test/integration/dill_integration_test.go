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

func TestDillRealEngineLifecycle(t *testing.T) {
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

	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-%d", engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")

	writeConfig(t, cfg, root, engine, stack, "one")
	t.Cleanup(func() {
		_ = runDill(t, false, bin, "-f", cfg, "down")
	})

	runDill(t, true, bin, "-f", cfg, "--timeout", "2m", "up")
	assertExecOutput(t, bin, cfg, "one\n")
	assertNoopPlan(t, bin, cfg, 1)

	writeConfig(t, cfg, root, engine, stack, "two")
	runDill(t, true, bin, "-f", cfg, "--timeout", "2m", "up")
	assertExecOutput(t, bin, cfg, "two\n")
	assertNoopPlan(t, bin, cfg, 1)

	runDill(t, true, bin, "-f", cfg, "down")
	assertEmptyStack(t, bin, cfg)
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

func assertExecOutput(t *testing.T, bin, cfg, want string) {
	t.Helper()
	out := runDill(t, true, bin, "-f", cfg, "exec", "app", "sh", "-c", "printf '%s\\n' \"$PUBLIC\"")
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
