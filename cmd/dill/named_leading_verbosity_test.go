package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedMainPreservesLiteralVerbosityFlagValues(t *testing.T) {
	for _, namespace := range []string{"preflight", "operation"} {
		t.Run(namespace, func(t *testing.T) {
			contract := preflight.Version
			if namespace == "operation" {
				contract = operation.Version
			}
			args := []string{"-test.run=^TestOperationMainHelper$", "--", namespace, "up", "--contract", contract, "-f", "-v", "-p", "app", "--engine", "docker", "--engine-socket", "/synthetic.sock", "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", "synthetic", "--coordination-state-dir", t.TempDir(), "--coordination-source", "cli"}
			if namespace == "operation" {
				args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", "sha256:"+strings.Repeat("a", 64))
			}
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1", "DILL_OPERATION_MAIN_RUNTIME=named-literal")
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.Len() != 0 {
				t.Fatalf("literal value rejected: %v %s %s", err, out.String(), stderr.String())
			}
			if namespace == "preflight" {
				var r preflight.Result
				if json.Unmarshal(out.Bytes(), &r) != nil || len(r.Errors) != 1 || r.Errors[0].Code != "EVALUATION_FAILED" {
					t.Fatalf("%s", out.String())
				}
			} else {
				var r operation.Result
				if json.Unmarshal(out.Bytes(), &r) != nil || len(r.Errors) != 1 || r.Errors[0].Code != "OPERATION_FAILED" {
					t.Fatalf("%s", out.String())
				}
			}
		})
	}
}

func TestNamedMainRejectsLeadingVerbosityBeforeNamespace(t *testing.T) {
	for _, namespace := range []string{"preflight", "operation"} {
		t.Run(namespace, func(t *testing.T) {
			contract := preflight.Version
			if namespace == "operation" {
				contract = operation.Version
			}
			args := []string{"-test.run=^TestOperationMainHelper$", "--", "-v", namespace, "up", "--contract", contract, "-f", "synthetic.pkl", "-p", "app", "--engine", "docker", "--engine-socket", "/synthetic.sock", "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", "synthetic", "--coordination-state-dir", t.TempDir(), "--coordination-source", "cli"}
			if namespace == "operation" {
				args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", "sha256:"+strings.Repeat("a", 64))
			}
			cmd := exec.Command(os.Args[0], args...)
			cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1")
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || stderr.Len() != 0 {
				t.Fatalf("wrong exit %v stderr=%s", err, stderr.String())
			}
			if namespace == "preflight" {
				var r preflight.Result
				if json.Unmarshal(out.Bytes(), &r) != nil || len(r.Errors) != 1 || r.Errors[0].Code != "INVALID_ARGUMENTS" {
					t.Fatalf("incorrect preflight result: %s", out.String())
				}
			} else {
				var r operation.Result
				if json.Unmarshal(out.Bytes(), &r) != nil || len(r.Errors) != 1 || r.Errors[0].Code != "INVALID_ARGUMENTS" {
					t.Fatalf("incorrect operation result: %s", out.String())
				}
			}
		})
	}
}
