//go:build integration

package integration

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDillRealEngineFailedStartCleanup(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-failstart-%s-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writeConfig(t, cfg, root, env.engine, stack, "seed")
	cleanupStack(t, bin, cfg, stack)
	// Ensure the test image is present using the same engine and normal pull policy.
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	runDillDown(t, env.engine, stack, bin, "-f", cfg, "down")
	blocker := stack + "-blocker"
	t.Cleanup(func() { _, _ = runEngineRaw(t, env.engine, "rm", "-f", blocker) })
	runEngine(t, env.engine, "run", "-d", "--name", blocker, "-p", "127.0.0.1::8080", "docker.io/library/busybox:1.36", "sleep", "300")
	address := runEngine(t, env.engine, "port", blocker, "8080/tcp")
	_, port, err := net.SplitHostPort(strings.TrimSpace(strings.Split(address, "\n")[0]))
	if err != nil {
		t.Fatalf("parse port %q: %v", address, err)
	}
	body := fmt.Sprintf(`amends "%s"
config {
 name = "%s"
 engine = "%s"
 services {
  ["good"] {
   image = "docker.io/library/busybox:1.36"
   pullPolicy = "missing"
   command = List("sleep", "300")
  }
  ["bad"] {
   image = "docker.io/library/busybox:1.36"
   pullPolicy = "missing"
   dependsOn = List("good")
   ports = List("127.0.0.1:%s:8080")
   command = List("sleep", "300")
  }
 }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, port)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-f", cfg, "--timeout", testTimeout(), "up").CombinedOutput()
	if err == nil {
		t.Fatalf("expected port conflict, got success: %s", out)
	}
	message := strings.ToLower(string(out))
	if !strings.Contains(message, "port") && !strings.Contains(message, "proxy already running") {
		t.Fatalf("expected port-conflict error: %s", out)
	}
	if got := stackContainerNames(t, env.engine, stack); got != "" {
		t.Fatalf("failed-start container or sibling survived rollback: %s\n%s", got, out)
	}
	if got := containerState(t, env.engine, blocker); got != "running" {
		t.Fatalf("foreign blocker state = %q", got)
	}
}
