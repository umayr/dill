package main

import (
	"context"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestPreviewFlagsRequireBothAndSecretPlan(t *testing.T) {
	oldLoad, oldEngine := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoad, oldEngine }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("invalid preview request contacted engine")
		return nil, nil
	}
	for _, args := range [][]string{{"plan", "--proof-file", "/synthetic/output"}, {"plan", "--proof-key-file", "/synthetic/key"}, {"plan", "--proof-file=", "--proof-key-file="}, {"plan", "--proof-file", "relative", "--proof-key-file", "/synthetic/key"}, {"plan", "--proof-file", "/synthetic/output", "--proof-key-file", "/synthetic/key"}, {"--proof-file", "/synthetic/output", "--proof-key-file", "/synthetic/key", "up"}, {"--proof-file", "/synthetic/output", "--proof-key-file", "/synthetic/key", "diff"}} {
		out, stderr, err := captureCommand(t, "", func() error { return run(args) })
		if err != config.ErrSecretUnavailable || strings.Contains(out+stderr, "synthetic") {
			t.Fatalf("unsafe invalid preview: %v %q %q", err, out, stderr)
		}
	}
}
