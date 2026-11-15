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

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedMainStrictBeforeVerbosity(t *testing.T) {
	cases := [][]string{
		{"preflight", "up", "-v", "--contract", preflight.Version},
		{"operation", "up", "-v", "--contract", operation.Version, "--preflight-contract", preflight.Version, "--expect-preflight", "bad"},
	}
	for _, args := range cases {
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
			t.Fatalf("args=%v err=%v stderr=%s", args, err, stderr.String())
		}
		if args[0] == "preflight" {
			var r preflight.Result
			if json.Unmarshal(stdout.Bytes(), &r) != nil || r.Errors[0].Code != "INVALID_ARGUMENTS" {
				t.Fatalf("bad preflight %s", stdout.String())
			}
		} else {
			var r operation.Result
			if json.Unmarshal(stdout.Bytes(), &r) != nil || r.Errors[0].Code != "INVALID_ARGUMENTS" {
				t.Fatalf("bad bound %s", stdout.String())
			}
		}
	}
}
func TestNamedMainDiscoveryIsEngineFree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOperationMainHelper$", "--", "preflight-capabilities", "--format", "json")
	cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1", "DILL_PKL_PATH=/nonexistent/private", "DOCKER_HOST=tcp://invalid")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("%v %s", err, stderr.String())
	}
	var c preflight.Capabilities
	if err := json.Unmarshal(stdout.Bytes(), &c); err != nil || c.Version != preflight.CapabilitiesVersion {
		t.Fatalf("%s %v", stdout.String(), err)
	}
}
