//go:build integration

package integration

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The engine chooses how to represent a default wildcard binding, potentially
// as separate IPv4/IPv6 entries. The plan must converge for both adapters.
func TestDillPortBindingsConverge(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-ports-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	var listeners []net.Listener
	var ports []int
	for range 3 {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range listeners {
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
	}
	body := fmt.Sprintf(`amends %q
config {
  name = %q
  engine = %q
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      pullPolicy = "missing"
      command = List("sh", "-c", "trap 'exit 0' TERM; while true; do sleep 1; done")
      ports = List("127.0.0.1:%d:80", "127.0.0.1:%d:80", "%d:81")
    }
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, ports[0], ports[1], ports[2])
	if err := os.WriteFile(cfg, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)
}
