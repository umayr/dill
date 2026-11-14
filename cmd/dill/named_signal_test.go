package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedPreviewSignalProducesCancelledEnvelope(t *testing.T) {
	for name, sig := range map[string]os.Signal{"interrupt": os.Interrupt, "terminate": syscall.SIGTERM} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "evaluation-started")
			args := []string{"-test.run=^TestOperationMainHelper$", "--", "preflight", "up", "--contract", preflight.Version, "-f", "synthetic.pkl", "-p", "app", "--engine", "docker", "--engine-socket", "/synthetic.sock", "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", "synthetic-daemon", "--coordination-state-dir", t.TempDir(), "--coordination-source", "cli"}
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1", "DILL_OPERATION_MAIN_RUNTIME=preview-cancel", "DILL_OPERATION_MUTATION_MARKER="+marker)
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("evaluation did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.Len() != 0 {
				t.Fatalf("exit=%v stderr=%s", err, stderr.String())
			}
			var result preflight.Result
			if err = json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if err = result.Validate(1); err != nil || result.Outcome != "cancelled" || result.Errors[0].Code != "CANCELLED" || result.Target != nil || result.CoordinationEffects != "none" {
				t.Fatalf("bad cancelled result: %+v %v", result, err)
			}
		})
	}
}
