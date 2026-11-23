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

// A nonzero beforeUpdate job must never stop the working application. A fast
// exiting job also exercises observing completion after the process has exited.
func TestFailedBeforeUpdatePreservesRunningVersion(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-hooks-%d", time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	write := func(version, down string) {
		t.Helper()
		body := fmt.Sprintf(`amends %q
config {
 name = %q
 engine = %q
 jobs {
   ["finish"] { image = "docker.io/library/busybox:1.36"; command = List("true") }
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
     hooks { beforeUpdate { new { job = "reject" } }; beforeDown { new { job = "finish" } }; afterDown { new { job = %q } } }
   }
 }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, version, down)
		if err := os.WriteFile(cfg, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("old", "reject")
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertExecOutput(t, bin, cfg, "old\n")
	write("new", "reject")
	expectFailure := func(want string, args ...string) {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"-f", cfg, "--timeout", testTimeout()}, args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), want) {
			t.Fatalf("%v: err=%v output=%s", args, err, out)
		}
	}
	expectFailure("exited 3", "up")
	expectFailure("exited 3", "job", "reject")
	runDill(t, true, bin, "-f", cfg, "job", "finish")
	out, _ := exec.Command(bin, "-f", cfg, "plan", "--format", "json").Output()
	if !strings.Contains(string(out), `"beforeUp"`) || !strings.Contains(string(out), `"reject"`) {
		t.Fatalf("missing hook in plan: %s", out)
	}
	assertExecOutput(t, bin, cfg, "old\n")
	write("old", "finish")
	expectFailure("definition unavailable or changed", "down")
	assertExecOutput(t, bin, cfg, "old\n")
	write("old", "reject")
	expectFailure("afterDown failed", "down")
	assertEmptyStack(t, bin, cfg)
}
