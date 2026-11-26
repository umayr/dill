//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
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
	engine, err := client.NewClientWithOpts(client.WithHost(socket), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	info, err := engine.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	daemon := info.ID
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-managed-%d", time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writeConfig(t, cfg, root, "docker", stack, "one")
	// Cleanup below is restricted to the immutable IDs returned by this test's
	// managed creation, never a daemon-wide prune or label-name adoption.
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
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, r := range owned {
			if r.Kind == "container" {
				if e := engine.ContainerRemove(cleanup, r.ID, container.RemoveOptions{Force: true}); e != nil {
					t.Errorf("cleanup container: %v", e)
				}
			}
		}
		for _, r := range owned {
			if r.Kind == "network" {
				if e := engine.NetworkRemove(cleanup, r.ID); e != nil {
					t.Errorf("cleanup network: %v", e)
				}
			}
		}
	}()
	for _, action := range []string{"stop", "stop", "start", "restart"} {
		result := invoke(action, "update", owned)
		if !slices.Equal(*result.ManagedResources, owned) {
			t.Fatal("lifecycle changed immutable ownership")
		}
	}
}
