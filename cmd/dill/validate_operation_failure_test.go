package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// FindPkl caches its first result process-wide, so tests selecting a Pkl binary
// must run in separate processes to avoid contaminating other command tests.
func isolatePklTest(t *testing.T) bool {
	t.Helper()
	if os.Getenv("DILL_OPERATION_TEST_CHILD") == t.Name() {
		return false
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), "DILL_OPERATION_TEST_CHILD="+t.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated test: %v\n%s", err, out)
	}
	return true
}

func TestValidateOperationV1EvaluatorFailure(t *testing.T) {
	if isolatePklTest(t) {
		return
	}
	t.Setenv("DILL_PKL_PATH", filepath.Join(t.TempDir(), "missing"))
	path := filepath.Join(t.TempDir(), "compose.pkl")
	if err := os.WriteFile(path, []byte("SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureCommand(t, "", func() error {
		return runValidate(context.Background(), commandOptions{ConfigFile: path}, []string{"--format", "operation-v1"})
	})
	if err == nil {
		t.Fatal("expected evaluator failure")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["outcome"] != "failed" || got["engineEffects"] != "none" || got["stack"] != "" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestValidateOperationCancelledEvaluation(t *testing.T) {
	if isolatePklTest(t) {
		return
	}
	pkl := filepath.Join(t.TempDir(), "pkl")
	if err := os.WriteFile(pkl, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DILL_PKL_PATH", pkl)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, _, err := captureCommand(t, "", func() error {
		return runValidate(ctx, commandOptions{ConfigFile: "missing"}, []string{"--format", "operation-v1"})
	})
	if err == nil {
		t.Fatal("expected cancellation")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["outcome"] != "cancelled" {
		t.Fatalf("outcome=%q, want cancelled (err: %v)", got["outcome"], err)
	}
}

func TestValidateOperationCancelledContextDoesNotMaskEvaluatorSetupFailure(t *testing.T) {
	if isolatePklTest(t) {
		return
	}
	t.Setenv("DILL_PKL_SHA256", "unsupported")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, _, err := captureCommand(t, "", func() error {
		return runValidate(ctx, commandOptions{ConfigFile: "missing"}, []string{"--format", "operation-v1"})
	})
	if err == nil {
		t.Fatal("expected evaluator setup failure")
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["outcome"] != "failed" {
		t.Fatalf("outcome=%q, want failed (err: %v)", got["outcome"], err)
	}
}

func TestValidateOperationUnsupportedVersionDoesNotEvaluate(t *testing.T) {
	t.Setenv("DILL_PKL_PATH", filepath.Join(t.TempDir(), "missing"))
	out, _, err := captureCommand(t, "", func() error {
		return runValidate(context.Background(), commandOptions{ConfigFile: "missing"}, []string{"--format", "operation-v2"})
	})
	if exitCodeFor(err) != 2 || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
