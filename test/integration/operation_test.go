//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/operation"
)

// This test needs the real pinned evaluator, but never connects to an engine.
// Missing/wrong Pkl is a failure, not a skipped conformance claim.
func TestOperationRealPklClassification(t *testing.T) {
	root := repoRoot(t)
	bin := dillBinary(t, root)
	pkl := os.Getenv("DILL_PKL_PATH")
	if pkl == "" {
		var err error
		pkl, err = exec.LookPath("pkl")
		if err != nil {
			t.Fatal("pinned Pkl required")
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/loader/pkl-toolchain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var toolchain struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &toolchain) != nil {
		t.Fatal("invalid toolchain pin")
	}
	version, err := exec.Command(pkl, "--version").Output()
	if err != nil || !strings.Contains(string(version), "Pkl "+toolchain.Version+" ") {
		t.Fatalf("expected pinned Pkl %s", toolchain.Version)
	}
	for _, adapter := range []string{"docker", "podman"} {
		for _, tc := range []struct {
			name, source, outcome, code, timeout string
			exit                                 int
		}{
			{"invalid graph", fmt.Sprintf("amends %q\nconfig { name = \"app\"; engine = %q; services { [\"web\"] { image = \"busybox\"; dependsOn = List(\"MISSING_SENTINEL\") } } }\n", filepath.ToSlash(filepath.Join(root, "dill.pkl")), adapter), "invalid_input", "INVALID_INPUT", "2s", 2},
			{"evaluator failure", "SYNTAX_SENTINEL !??", "failed", "OPERATION_FAILED", "2s", 1},
			{"cancelled evaluator", fmt.Sprintf("amends %q\nconfig { name = \"app\"; services { [\"web\"] { image = \"busybox\" } } }\n", filepath.ToSlash(filepath.Join(root, "dill.pkl"))), "cancelled", "CANCELLED", "1ms", 1},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "PATH_SENTINEL.pkl")
				if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bin, "operation", "validate", "--contract", operation.Version, "--engine", adapter, "--engine-socket", "/synthetic/NO_ENGINE_SENTINEL.sock", "-p", "app", "-f", path, "--timeout", tc.timeout)
				cmd.Env = append(os.Environ(), "DILL_PKL_PATH="+pkl)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != tc.exit {
					t.Fatalf("exit=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
				}
				var result operation.Result
				if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Outcome != tc.outcome || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != tc.code {
					t.Fatalf("wrong actual Pkl outcome: %s", stdout.String())
				}
				if stderr.Len() != 0 || strings.Contains(stdout.String(), "SENTINEL") {
					t.Fatal("private evaluator leaked identities/diagnostics")
				}
			})
		}
	}
}
