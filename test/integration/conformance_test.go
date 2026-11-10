//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Invalid full configurations must fail before any selected or unselected
// container, or the stack network, is replaced or stopped.
func TestConformanceInvalidPartialSelectionPreservesStack(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-conf-%s-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writePartialConfig(t, cfg, root, env.engine, stack, true)
	cleanupStack(t, bin, cfg, stack)
	// Restore a valid config before cleanupStack runs (cleanups are LIFO).
	t.Cleanup(func() { writePartialConfig(t, cfg, root, env.engine, stack, true) })
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	names := []string{stack + "_app", stack + "_dep", stack + "_other"}
	ids := make(map[string]string)
	for _, name := range names {
		ids[name] = runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name)
	}
	network := runEngine(t, env.engine, "network", "inspect", "--format", "{{.Id}}", stack)
	body := fmt.Sprintf(`amends %q
config {
 name = %q
 engine = %q
 services {
  ["app"] { image = "docker.io/library/busybox:1.36"; dependsOn = List("dep") }
  ["dep"] { image = "docker.io/library/busybox:1.36" }
  ["other"] { image = "docker.io/library/busybox:1.36"; dependsOn = List("other") }
 }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine)
	if err := os.WriteFile(cfg, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"up", "app"}, {"up", "--no-deps", "app"}, {"up", "--dry-run", "app"}} {
		out, err := exec.Command(bin, append([]string{"-f", cfg}, args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cycle") {
			t.Fatalf("%v: expected cycle error, got %v: %s", args, err, out)
		}
		for _, name := range names {
			if id := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name); id != ids[name] {
				t.Fatalf("%v replaced %s", args, name)
			}
			if state := containerState(t, env.engine, name); state != "running" {
				t.Fatalf("%v changed %s state to %s", args, name, state)
			}
		}
		if got := runEngine(t, env.engine, "network", "inspect", "--format", "{{.Id}}", stack); got != network {
			t.Fatalf("%v replaced network", args)
		}
	}
}
