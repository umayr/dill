//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDillResourceLimits(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-resources-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	write := func(memory string, limits bool) {
		t.Helper()
		resources := ""
		if limits {
			resources = fmt.Sprintf(`
      memLimit = "%s"
      memReservation = "32m"
      cpus = "0.123456789"
      cpuShares = 512
      pidsLimit = 100`, memory)
		}
		body := fmt.Sprintf(`amends "%s"
config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      command = List("sleep", "300")%s
    }
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, resources)
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("128m", true)
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)

	// External updates leave dill.config-hash unchanged. Planning must still
	// detect resource drift and up must restore the requested limit.
	name := stack + "_app"
	runEngine(t, env.engine, "update", "--memory", "256m", name)
	out := runDill(t, false, bin, "-f", cfg, "plan", "--format", "json")
	if !strings.Contains(out, `"memLimit"`) {
		t.Fatalf("expected live memory drift, got %s", out)
	}
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)

	// Desired changes and removal of limits must also converge to a no-op.
	write("192m", true)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)
	write("", false)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)
}
