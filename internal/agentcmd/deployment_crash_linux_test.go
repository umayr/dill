package agentcmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/umayr/dill/internal/operation"
)

// Exercise the ambiguous window with actual process death and a persistent
// Docker effect. The helper dies before it can persist an outcome.
func TestCrashAfterDockerEffect(t *testing.T) {
	if os.Getenv("DILL_TEST_DISPOSABLE_CRASH") != "1" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" {
		t.Skip("requires disposable CI Docker daemon")
	}
	id := strings.Repeat("1", 32)
	scope := operation.Digest("disposable crash test")
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: id, Manifest: strings.Repeat("a", 64)}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if root := os.Getenv("DILL_CRASH_TEST_ROOT"); root != "" {
		h, err := openDeploymentHistory(root, scope)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.reserve(row, ""); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.CommandContext(ctx, "docker", "--context", "default", "volume", "create", os.Getenv("DILL_CRASH_TEST_VOLUME")).CombinedOutput(); err != nil {
			t.Fatalf("create fixture: %v %s", err, out)
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := historyDirectory(t)
	name := "dill-crash-" + strings.ToLower(time.Now().UTC().Format("20060102t150405.000000000"))
	defer func() {
		if out, err := exec.Command("docker", "--context", "default", "volume", "rm", name).CombinedOutput(); err != nil {
			t.Errorf("clean fixture: %s", out)
		}
	}()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashAfterDockerEffect$")
	child.Env = append(os.Environ(), "DILL_CRASH_TEST_ROOT="+root, "DILL_CRASH_TEST_VOLUME="+name)
	out, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL || ctx.Err() != nil {
		t.Fatalf("helper did not reach crash window: %v %s", err, out)
	}
	if err := exec.CommandContext(ctx, "docker", "--context", "default", "volume", "inspect", name).Run(); err != nil {
		t.Fatal("lost evidence of completed engine effect", err)
	}
	h, err := openDeploymentHistory(root, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	got, ok := h.status(id)
	if !ok || got.State != "unknown" || got.Effects != "unknown" {
		t.Fatal("crash was not reported as unknown")
	}
	if h.reserve(row, "") == nil {
		t.Fatal("duplicate request admitted")
	}
	row.RequestID = strings.Repeat("2", 32)
	if h.reserve(row, "") == nil {
		t.Fatal("fresh request admitted for unresolved stack")
	}
}
