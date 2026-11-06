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

const ownershipImage = "docker.io/library/busybox:1.36"

func writeOwnershipConfig(t *testing.T, path, root, engine, stack, containerName, pullPolicy, duration string) {
	t.Helper()
	nameProperty := ""
	if containerName != "" {
		nameProperty = fmt.Sprintf("containerName = %q", containerName)
	}
	body := fmt.Sprintf(`amends %q
config {
  name = %q
  engine = %q
  services {
    ["app"] {
      image = %q
      %s
      pullPolicy = %q
      command = List("sleep", %q)
    }
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, engine, ownershipImage, nameProperty, pullPolicy, duration)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDillForeignContainerOwnership(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	runEngine(t, env.engine, "pull", ownershipImage)

	for _, customName := range []bool{false, true} {
		for _, kind := range []string{"unmanaged", "managed-false", "missing-stack", "missing-service", "other-stack", "other-service"} {
			t.Run(fmt.Sprintf("custom-%v/%s", customName, kind), func(t *testing.T) {
				stack := fmt.Sprintf("dill-own-%d", time.Now().UnixNano())
				name := stack + "_app"
				configuredName := ""
				if customName {
					name = stack + "-custom"
					configuredName = name
				}
				labels := map[string]string{"dill.managed": "true", "dill.stack": stack, "dill.service": "app"}
				switch kind {
				case "unmanaged":
					labels = nil
				case "managed-false":
					labels["dill.managed"] = "false"
				case "missing-stack":
					delete(labels, "dill.stack")
				case "missing-service":
					delete(labels, "dill.service")
				case "other-stack":
					labels["dill.stack"] = stack + "-other"
				case "other-service":
					labels["dill.service"] = "other"
				}
				args := []string{"run", "-d", "--stop-timeout", "1", "--name", name}
				for key, value := range labels {
					args = append(args, "--label", key+"="+value)
				}
				args = append(args, ownershipImage, "sleep", "300")
				id := runEngine(t, env.engine, args...)
				t.Cleanup(func() {
					if out, err := runEngineRaw(t, env.engine, "rm", "-f", id); err != nil {
						t.Errorf("cleaning fixture %s: %v: %s", id, err, out)
					}
					_, _ = runEngineRaw(t, env.engine, "network", "rm", stack)
				})
				cfg := filepath.Join(t.TempDir(), "compose.pkl")
				writeOwnershipConfig(t, cfg, root, env.engine, stack, configuredName, "always", "300")
				for _, command := range [][]string{{"plan"}, {"diff"}, {"up"}, {"up", "--force-recreate"}, {"up", "--dry-run"}} {
					cmd := exec.Command(bin, append([]string{"-f", cfg}, command...)...)
					out, err := cmd.CombinedOutput()
					if err == nil || !strings.Contains(string(out), "container ownership conflict") {
						t.Fatalf("%v: expected failed ownership check, got %v: %s", command, err, out)
					}
					gotID := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name)
					if gotID != id {
						t.Fatalf("foreign container replaced: %s -> %s", id, gotID)
					}
					if state := containerState(t, env.engine, id); state != "running" {
						t.Fatalf("foreign container state = %s, want running", state)
					}
				}
			})
		}
	}
}

func TestDillOwnedContainerReconciles(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-owned-%d", time.Now().UnixNano())
	name := stack + "-custom"
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writeOwnershipConfig(t, cfg, root, env.engine, stack, name, "missing", "300")
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "up")
	id := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name)
	assertNoopPlan(t, bin, cfg, 1)
	runDill(t, true, bin, "-f", cfg, "up")
	if got := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name); got != id {
		t.Fatal("matching container unexpectedly recreated")
	}
	runDill(t, true, bin, "-f", cfg, "up", "--force-recreate")
	forcedID := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name)
	if forcedID == id {
		t.Fatal("force-recreate did not replace owned container")
	}
	writeOwnershipConfig(t, cfg, root, env.engine, stack, name, "missing", "301")
	runDill(t, true, bin, "-f", cfg, "up")
	if got := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name); got == forcedID {
		t.Fatal("changed owned container was not recreated")
	}
	assertNoopPlan(t, bin, cfg, 1)
}
