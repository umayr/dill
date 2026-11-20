package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func TestPlanDiffMissingEnvFileJSON(t *testing.T) {
	oldLoader := configLoader
	defer func() { configLoader = oldLoader }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		t.Fatal("evaluator must not run for a missing env file")
		return nil, nil
	}
	path := filepath.Join(t.TempDir(), "missing.env")
	for _, command := range []string{"plan", "diff"} {
		for _, global := range []bool{true, false} {
			args := []string{command, "--env-file", path, "--format", "json"}
			if global {
				args = []string{"--env-file", path, command, "--format", "json"}
			}
			out, _, err := captureCommand(t, "", func() error { return run(args) })
			var result struct {
				Kind  string `json:"kind"`
				Error string `json:"error"`
			}
			if exitCodeFor(err) != 1 || json.Unmarshal([]byte(out), &result) != nil || result.Kind != "operational_failure" || !strings.Contains(result.Error, "loading env file") {
				t.Errorf("args=%v err=%v output=%q", args, err, out)
			}
		}
	}
}
