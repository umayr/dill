package main

import (
	"context"
	"errors"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestUpRejectsMissingSecretBindingsBeforeEngine(t *testing.T) {
	cfg := &config.DillConfig{Name: "demo", Engine: "docker", Secrets: []config.SecretRequirement{{Name: "db"}}, Services: map[string]*config.Service{"app": {Image: "alpine", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", Mode: 256}}}}}
	opened := false
	opts := secretOptions()
	opts.SecretBindings = ""
	opts.SecretBindingsSet = false
	err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return nil, nil })
	if !errors.Is(err, config.ErrSecretUnavailable) || opened {
		t.Fatalf("err=%v opened=%v", err, opened)
	}
}
