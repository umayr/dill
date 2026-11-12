package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOperationV1EvaluatorFailure(t *testing.T) {
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

func TestValidateOperationUnsupportedVersionDoesNotEvaluate(t *testing.T) {
	t.Setenv("DILL_PKL_PATH", filepath.Join(t.TempDir(), "missing"))
	out, _, err := captureCommand(t, "", func() error {
		return runValidate(context.Background(), commandOptions{ConfigFile: "missing"}, []string{"--format", "operation-v2"})
	})
	if exitCodeFor(err) != 2 || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
