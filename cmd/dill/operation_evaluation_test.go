package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/operation"
)

// Unlike configLoader stubs, these cases enter real config.Load, launch the
// bounded private subprocess, decode its output, and retain typed categories.
func TestOperationMachineLoadSubprocessCategories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX synthetic evaluator fixture")
	}
	for _, tc := range []struct {
		name, body, outcome, code string
		exit                      int
		timeout                   string
	}{
		{"invalid graph", `printf '%s' '{"config":{"name":"app","engine":"docker","services":{"web":{"image":"busybox","dependsOn":["MISSING_SENTINEL"]}}}}'`, "invalid_input", "INVALID_INPUT", 2, "2s"},
		{"evaluator failure", `printf DIAGNOSTIC_SENTINEL >&2; exit 1`, "failed", "OPERATION_FAILED", 1, "2s"},
		{"cancelled evaluator", `exec sleep 30`, "cancelled", "CANCELLED", 1, "100ms"},
		{"decoded secret grants", `printf '%s' '{"config":{"name":"app","engine":"docker","services":{"web":{"image":"busybox","secrets":[{"name":"SECRET_SENTINEL","target":"/run/secrets/value","uid":0,"gid":0,"mode":256}]}}}}'`, "failed", "SECRET_UNAVAILABLE", 1, "2s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evaluator := filepath.Join(t.TempDir(), "EVALUATOR_SENTINEL")
			if err := os.WriteFile(evaluator, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			args := operationArgs("validate", "docker", "--timeout", tc.timeout, "-f", "/PATH_SENTINEL/compose.pkl")
			cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestOperationMainHelper$", "--", "operation"}, args...)...)
			cmd.Env = append(os.Environ(), "DILL_OPERATION_MAIN_HELPER=1", "DILL_PKL_PATH="+evaluator)
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
				t.Fatalf("wrong typed private result: %s", stdout.String())
			}
			if stderr.Len() != 0 || strings.Contains(stdout.String(), "SENTINEL") {
				t.Fatalf("private diagnostic escaped: %s %s", stdout.String(), stderr.String())
			}
			if tc.code == "SECRET_UNAVAILABLE" && (result.Target != nil || result.Stack != "") {
				t.Fatal("secret error included identity")
			}
		})
	}
}
