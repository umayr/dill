package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/operation"
)

type operationPromptMarkerEngine struct {
	*fakeEngine
	marker string
}

func (e *operationPromptMarkerEngine) RemoveService(context.Context, string) error {
	return os.WriteFile(e.marker, []byte("mutation"), 0600)
}

type operationPromptOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (w *operationPromptOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buffer.Write(p)
	if strings.Contains(w.buffer.String(), "Continue?") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func TestOperationPromptSubprocessDeadlineAndSignal(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix attended prompt")
	}
	for _, signal := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "SIGINT"}[signal], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			timeout := "150ms"
			if signal {
				timeout = "2s"
			}
			args := operationArgs("rm", "docker", "--timeout", timeout)
			cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestOperationMainHelper$", "--", "operation"}, args...)...)
			marker := filepath.Join(t.TempDir(), "mutation-marker")
			cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1", "DILL_OPERATION_MAIN_RUNTIME=prompt", "DILL_OPERATION_MUTATION_MARKER="+marker)
			input, heldOpen, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer heldOpen.Close()
			cmd.Stdin = input
			var stdout bytes.Buffer
			stderr := &operationPromptOutput{ready: make(chan struct{})}
			cmd.Stdout = &stdout
			cmd.Stderr = stderr
			started := time.Now()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-stderr.ready:
			case <-ctx.Done():
				t.Fatal("prompt not reached")
			}
			if signal {
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			err = cmd.Wait()
			if err == nil || time.Since(started) > time.Second {
				t.Fatalf("prompt did not cancel promptly: %v elapsed %v", err, time.Since(started))
			}
			var result operation.Result
			if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Outcome != "cancelled" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "CANCELLED" {
				t.Fatalf("wrong terminal result: %s", stdout.String())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("prompt cancellation mutated engine")
			}
			// The parent still owns an open writer: cancellation never depended on EOF.
			if _, err := heldOpen.Stat(); err != nil {
				t.Fatal("caller input was closed")
			}
		})
	}
}
