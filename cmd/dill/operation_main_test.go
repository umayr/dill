package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestOperationMainHelper(t *testing.T) {
	if os.Getenv("DILL_OPERATION_MAIN_HELPER") != "1" {
		return
	}
	os.Unsetenv("DILL_SECRET_POLICY")
	if os.Getenv("DILL_OPERATION_MAIN_RUNTIME") == "prompt" {
		configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
		f := &operationPromptMarkerEngine{fakeEngine: newFakeEngine(), marker: os.Getenv("DILL_OPERATION_MUTATION_MARKER")}
		f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
		engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return f, nil }
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"dill"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	os.Exit(99)
}

func TestOperationMainProtocolValuesStayInsideNamespace(t *testing.T) {
	for _, args := range [][]string{
		{"operation", "up", "--contract", "dill.operation/999", "--engine", "docker", "--engine-socket", "/synthetic/engine.sock", "-p", "protocol"},
		{"operation", "up", "--contract", "dill.operation/999", "-f", "protocol"},
		{"operation", "up", "--contract", "dill.operation/999", "protocol"},
		{"operation", "up", "--contract", "protocol"},
		{"operation-capabilities", "protocol"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestOperationMainHelper$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 || stderr.Len() != 0 {
			t.Fatalf("args=%v exit=%v stdout=%s stderr=%s", args, err, stdout.String(), stderr.String())
		}
		if args[0] == "operation-capabilities" {
			if stdout.Len() != 0 {
				t.Fatal(stdout.String())
			}
			continue
		}
		var result operation.Result
		if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Version != operation.Version || len(result.Errors) != 1 || result.Errors[0].Code != "UNSUPPORTED_CONTRACT" || result.Effects != "none" {
			t.Fatalf("args=%v wrong namespace: %s", args, stdout.String())
		}
	}
}
