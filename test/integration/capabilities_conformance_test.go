//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConformanceCapabilitiesPreflight(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	out := runDill(t, true, bin, "--engine", env.engine, "caps")
	var caps struct {
		Engine      string   `json:"engine"`
		Devices     bool     `json:"devices"`
		Unpublished bool     `json:"unpublishedPorts"`
		Modes       []string `json:"networkModes"`
	}
	if err := json.Unmarshal([]byte(out), &caps); err != nil {
		t.Fatalf("discovery JSON: %v: %s", err, out)
	}
	if caps.Engine != env.engine || caps.Devices != (env.engine == "docker") || caps.Unpublished != (env.engine == "docker") {
		t.Fatalf("unexpected adapter capabilities: %s", out)
	}
	if env.engine == "docker" {
		if len(caps.Modes) != 0 {
			t.Fatalf("unexpected Docker modes: %s", out)
		}
		return
	}
	if strings.Join(caps.Modes, ",") != "host,none" {
		t.Fatalf("unexpected Podman modes: %s", out)
	}
	stack := fmt.Sprintf("dill-cap-%d", time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writePartialConfig(t, cfg, root, env.engine, stack, true)
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	names := []string{stack + "_app", stack + "_dep", stack + "_other"}
	ids := map[string]string{}
	for _, name := range names {
		ids[name] = runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name)
	}
	network := runEngine(t, env.engine, "network", "inspect", "--format", "{{.Id}}", stack)
	volume := stack + "_protected"
	runEngine(t, env.engine, "volume", "create", volume)
	t.Cleanup(func() {
		if out, err := runEngineRaw(t, env.engine, "volume", "rm", volume); err != nil {
			t.Errorf("cleanup volume %s: %v: %s", volume, err, out)
		}
	})
	for _, tc := range []struct{ name, field, diagnostic string }{
		{"device", `devices = List("/dev/null:/dev/null")`, "device mappings"},
		{"network", `networkMode = "container:other"`, "network mode"},
		{"port", `ports = List("80")`, "unpublished port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`amends %q
config {
 name = %q
 engine = %q
 services {
  ["app"] {
   image = "localhost:1/dill-cap-never-pull:missing"
   pullPolicy = "always"
   volumes = List("protected:/data")
   %s
  }
 }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, tc.field)
			if err := os.WriteFile(cfg, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"up"}, {"up", "app"}, {"up", "--dry-run", "app"}} {
				output, err := exec.Command(bin, append([]string{"-f", cfg}, args...)...).CombinedOutput()
				if err == nil || !strings.Contains(string(output), tc.diagnostic) {
					t.Fatalf("%v: expected %s: %v: %s", args, tc.diagnostic, err, output)
				}
				for _, name := range names {
					if id := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", name); id != ids[name] {
						t.Fatalf("%v replaced %s", args, name)
					}
					if state := containerState(t, env.engine, name); state != "running" {
						t.Fatalf("%v stopped %s: %s", args, name, state)
					}
				}
				if got := runEngine(t, env.engine, "network", "inspect", "--format", "{{.Id}}", stack); got != network {
					t.Fatalf("%v replaced network", args)
				}
				if !volumeExists(t, env.engine, volume) {
					t.Fatalf("%v removed volume", args)
				}
			}
		})
	}
}
