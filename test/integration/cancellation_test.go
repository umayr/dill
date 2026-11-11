//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type cancellationOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	waiting  chan struct{}
	waitSeen bool
}

func (o *cancellationOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.buffer.Write(p)
	if !o.waitSeen && strings.Contains(o.buffer.String(), "waiting (") {
		o.waitSeen = true
		close(o.waiting)
	}
	return n, err
}
func (o *cancellationOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}

// Exercise the same timeout and signal-driven rollback on Docker and Podman.
// Waiting progress proves StartService succeeded and the container was
// registered for rollback before cancellation is delivered.
func TestDillCancelledUpRollsBack(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	// Pull outside the deadline so the timeout exercises readiness rollback,
	// rather than cancelling an in-flight image pull or container creation.
	runEngine(t, env.engine, "pull", "docker.io/library/busybox:1.36")
	for _, mode := range []string{"timeout", "SIGTERM"} {
		t.Run(mode, func(t *testing.T) {
			stack := fmt.Sprintf("dill-cancel-%s-%d", env.engine, time.Now().UnixNano())
			cfg := filepath.Join(t.TempDir(), "compose.pkl")
			body := fmt.Sprintf(`amends %q
config {
  name = %q
  engine = %q
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      pullPolicy = "missing"
      command = List("sh", "-c", "trap 'exit 0' TERM; while true; do sleep 1; done")
      healthcheck {
        test = "false"
        interval = 30.s
        timeout = 1.s
        retries = 10
        startPeriod = 60.s
      }
    }
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine)
			if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			cleanupStack(t, bin, cfg, stack)
			t.Cleanup(func() { _, _ = runEngineRaw(t, env.engine, "network", "rm", stack) })
			timeout := "30s"
			if mode == "timeout" {
				timeout = "15s"
			}
			cmd := exec.Command(bin, "-f", cfg, "up", "--timeout", timeout)
			output := &cancellationOutput{waiting: make(chan struct{})}
			cmd.Stdout, cmd.Stderr = output, output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() { done <- cmd.Wait(); close(finished) }()
			t.Cleanup(func() {
				select {
				case <-finished:
					return
				default:
					_ = cmd.Process.Kill()
					<-finished
				}
			})
			if mode == "SIGTERM" {
				select {
				case <-output.waiting:
				case err := <-done:
					t.Fatalf("up ended before readiness wait: %v\n%s", err, output.String())
				case <-time.After(30 * time.Second):
					t.Fatalf("up never reached readiness wait\n%s", output.String())
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("cancelled deployment succeeded\n%s", output.String())
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("up did not exit after cancellation\n%s", output.String())
			}
			select {
			case <-output.waiting:
			default:
				t.Fatalf("cancelled before readiness wait; rollback was not exercised\n%s", output.String())
			}
			if !strings.Contains(output.String(), "deployment failed") {
				t.Fatalf("deployment did not reach rollback\n%s", output.String())
			}
			if names := stackContainerNames(t, env.engine, stack); names != "" {
				t.Fatalf("rollback left containers %s\n%s", names, output.String())
			}
		})
	}
}
