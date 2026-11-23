//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A nonzero beforeUpdate job must never stop the working application. A fast
// exiting job also exercises observing completion after the process has exited.
func TestFailedBeforeUpdatePreservesRunningVersion(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-hooks-%d", time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	write := func(version string) {
		t.Helper()
		body := fmt.Sprintf(`amends %q
config {
 name = %q
 engine = %q
 jobs {
   ["reject"] {
     image = "docker.io/library/busybox:1.36"
     command = List("sh", "-c", "exit 3")
   }
 }
 services {
   ["app"] {
     image = "docker.io/library/busybox:1.36"
     command = List("sleep", "300")
     environment { ["PUBLIC"] = %q }
     hooks { beforeUpdate { new { job = "reject" } } }
   }
 }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, version)
		if err := os.WriteFile(cfg, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("old")
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertExecOutput(t, bin, cfg, "old\n")
	write("new")
	runDill(t, false, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertExecOutput(t, bin, cfg, "old\n")
}
