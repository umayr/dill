//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDillResourceLimits(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-it-%s-resources-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	write := func(memory string, limits bool) {
		t.Helper()
		resources := ""
		if limits {
			resources = fmt.Sprintf(`
      memLimit = "%s"
      memReservation = "32m"
      cpus = "0.123456789"
      cpuShares = 512
      pidsLimit = 100`, memory)
		}
		body := fmt.Sprintf(`amends "%s"
config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "docker.io/library/busybox:1.36"
      command = List("sleep", "300")%s
    }
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, resources)
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assertInspectedLimits := func(memory int64) {
		t.Helper()
		// Inspect confirms the engine's reported configuration, not cgroup
		// enforcement. Use the same fixture for Docker and Podman.
		var got struct {
			Memory            int64  `json:"Memory"`
			MemoryReservation int64  `json:"MemoryReservation"`
			NanoCPUs          int64  `json:"NanoCpus"`
			CPUShares         int64  `json:"CpuShares"`
			PidsLimit         *int64 `json:"PidsLimit"`
		}
		out := runEngine(t, env.engine, "inspect", "--format", "{{json .HostConfig}}", stack+"_app")
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("parsing inspected resource limits: %v", err)
		}
		if got.Memory != memory || got.MemoryReservation != 32<<20 || got.NanoCPUs != 123450000 || got.CPUShares != 512 || got.PidsLimit == nil || *got.PidsLimit != 100 {
			t.Fatalf("inspected resource limits = %+v, want memory=%d reservation=%d nanoCPUs=123450000 shares=512 pids=100", got, memory, 32<<20)
		}
	}
	write("128m", true)
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertInspectedLimits(128 << 20)
	assertNoopPlan(t, bin, cfg, 1)

	// Preserve the ownership/hash labels while replacing the container with
	// different resources. Unlike `podman update`, this is observable through
	// inspect on Podman 4.x too: its update command changes the cgroup but
	// leaves the inspected OCI spec stale. Do not skip the drift assertion.
	name := stack + "_app"
	var labels map[string]string
	labelsJSON := runEngine(t, env.engine, "inspect", "--format", "{{json .Config.Labels}}", name)
	if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
		t.Fatalf("parsing container labels: %v", err)
	}
	if labels["dill.config-hash"] == "" {
		t.Fatal("expected the original configuration hash label")
	}
	runEngine(t, env.engine, "rm", "--force", name)
	args := []string{"run", "--detach", "--name", name,
		"--memory", "256m", "--memory-reservation", "32m",
		"--cpus", "0.12345", "--cpu-shares", "512", "--pids-limit", "100"}
	for key, value := range labels {
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, "docker.io/library/busybox:1.36", "sleep", "300")
	runEngine(t, env.engine, args...)
	out := runDill(t, false, bin, "-f", cfg, "plan", "--format", "json")
	if !strings.Contains(out, `"memLimit"`) {
		t.Fatalf("expected live memory drift, got %s", out)
	}
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)

	// Desired changes and removal of limits must also converge to a no-op.
	write("192m", true)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertInspectedLimits(192 << 20)
	assertNoopPlan(t, bin, cfg, 1)
	write("", false)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")
	assertNoopPlan(t, bin, cfg, 1)
}
