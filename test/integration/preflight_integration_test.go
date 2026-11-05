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

// An invalid desired graph must not delete a running service that has become
// an orphan, even when pullPolicy would otherwise trigger a registry request.
func TestDillRealEngineUpPreflightPreservesOrphans(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-preflight-%s-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	writePartialConfig(t, cfg, root, env.engine, stack, true)
	cleanupStack(t, bin, cfg, stack)
	runDill(t, true, bin, "-f", cfg, "--timeout", testTimeout(), "up")

	orphan := stack + "_other"
	id := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", orphan)
	if got := containerState(t, env.engine, orphan); got != "running" {
		t.Fatalf("orphan initial state = %q, want running", got)
	}

	for _, tc := range []struct {
		name, dependencies, want string
	}{
		{"cycle", "dependsOn = List(\"app\")", "cycle detected"},
		{"missing", "dependsOn = List(\"missing\")", "is not defined"},
	} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry-run=%t", tc.name, dry), func(t *testing.T) {
				body := fmt.Sprintf(`amends "%s"
config {
  name = "%s"
  engine = "%s"
  services {
    ["app"] {
      image = "localhost:1/dill-preflight-never-pull:nonexistent"
      pullPolicy = "always"
      %s
      command = List("sleep", "300")
    }
  }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, tc.dependencies)
				if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-f", cfg, "--timeout", testTimeout(), "up"}
				if dry {
					args = append(args, "--dry-run")
				}
				out, err := exec.Command(bin, args...).CombinedOutput()
				if err == nil || !strings.Contains(string(out), tc.want) {
					t.Fatalf("up error = %v; want %q\noutput:\n%s", err, tc.want, out)
				}
				if got := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", orphan); got != id {
					t.Fatalf("orphan ID = %q, want %q", got, id)
				}
				if got := containerState(t, env.engine, orphan); got != "running" {
					t.Fatalf("orphan state = %q, want running", got)
				}
			})
		}
	}
}
