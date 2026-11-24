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

func TestCompletionDependencyOnRealEngine(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-dep-%d", time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	write := func(exit int) {
		t.Helper()
		s := fmt.Sprintf(`amends %q
config {
 name = %q
 engine = %q
 services {
 ["migration"] { image = "docker.io/library/busybox:1.36"; command = List("sh", "-c", "exit %d") }
 ["app"] { image = "docker.io/library/busybox:1.36"; command = List("sleep", "300"); dependsOn = List(new Dependency { service = "migration"; condition = "completedSuccessfully" }) }
 }
}`, filepath.Join(root, "dill.pkl"), stack, env.engine, exit)
		if err := os.WriteFile(cfg, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(7)
	cleanupStack(t, bin, cfg, stack)
	args := []string{"-f", cfg, "--timeout", testTimeout(), "up"}
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "status 7") {
		t.Fatalf("did not observe failed dependency: %s %v", out, err)
	}
	if _, err := runEngineRaw(t, env.engine, "inspect", stack+"_app"); err == nil {
		t.Fatal("launched blocked app")
	}
	write(0)
	runDill(t, true, bin, args...)
	if containerState(t, env.engine, stack+"_app") != "running" {
		t.Fatal("app not started")
	}
	start := func() string {
		return runEngine(t, env.engine, "inspect", "--format", "{{.State.StartedAt}}", stack+"_migration")
	}
	before := start()
	runDill(t, true, bin, args...)
	if start() != before {
		t.Fatal("replayed completed dependency")
	}
}
