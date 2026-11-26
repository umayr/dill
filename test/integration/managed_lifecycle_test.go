//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// This exercises the private combined-agent child contract against a disposable
// Docker endpoint. It is not evidence of a Podman agent protocol.
func TestDillRealEngineManagedLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("combined-agent named coordination requires native Linux; use the disposable Linux qualification target")
	}
	env := integrationEnv(t)
	if env.engine != "docker" {
		t.Skip("combined-agent managed contract currently Docker only")
	}
	socket := os.Getenv("DOCKER_HOST")
	if !strings.HasPrefix(socket, "unix://") {
		t.Fatal("explicit disposable DOCKER_HOST=unix://... required")
	}
	daemonBytes, err := exec.Command("docker", "--host", socket, "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	daemon := strings.TrimSpace(string(daemonBytes))
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-managed-%d", time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writeConfig(t, cfg, root, "docker", stack, "one")
	cleanupStack(t, bin, cfg, stack)
	state := t.TempDir()
	os.Chmod(state, 0700)
	invoke := func(action, mode string, owned []operation.ManagedResource) operation.Result {
		t.Helper()
		args := []string{"preflight", action, "--contract", preflight.Version, "-f", cfg, "-p", stack, "--engine", "docker", "--engine-socket", socket, "--coordination-layout", coordination.NativeDockerLayout, "--coordination-daemon-id", daemon, "--coordination-state-dir", state, "--coordination-source", "brine", "--timeout", "2m"}
		if action == "up" {
			args = append(args, "--no-prune", "--pull", "never")
		}
		out, e := exec.Command(bin, args...).CombinedOutput()
		var preview preflight.Result
		if e != nil || json.Unmarshal(out, &preview) != nil || preview.SelectionDigest == nil {
			t.Fatalf("preview %s %v %s", action, e, out)
		}
		args[0] = "_managed-operation"
		args[3] = operation.Version
		args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", *preview.SelectionDigest)
		body, _ := json.Marshal(operation.ManagedExpectation{Mode: mode, Resources: owned})
		cmd := exec.Command(bin, args...)
		cmd.Stdin = bytes.NewReader(body)
		out, e = cmd.CombinedOutput()
		var result operation.Result
		if e != nil || json.Unmarshal(out, &result) != nil || result.Outcome != "succeeded" || result.ManagedResources == nil {
			t.Fatalf("managed %s %v %s", action, e, out)
		}
		return result
	}
	created := invoke("up", "create", []operation.ManagedResource{})
	owned := *created.ManagedResources
	for _, action := range []string{"stop", "stop", "start", "restart"} {
		result := invoke(action, "update", owned)
		if !slices.Equal(*result.ManagedResources, owned) {
			t.Fatal("lifecycle changed immutable ownership")
		}
	}
}
