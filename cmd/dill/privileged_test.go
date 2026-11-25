package main

import (
	"context"
	"errors"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"testing"
)

func TestPrivilegeDeniedBeforeOpen(t *testing.T) {
	cfg := testConfig()
	for _, svc := range cfg.Services {
		svc.Privileged = true
	}
	for _, opts := range []commandOptions{{}, {allowPrivileged: true}, {EngineSocketSet: true}, {allowPrivileged: true, EngineSocketSet: true, operation: &operationState{}}} {
		err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) {
			t.Fatal("opened engine")
			return nil, nil
		})
		if !errors.Is(err, orchestrator.ErrPrivilegeDenied) {
			t.Fatalf("got %v", err)
		}
	}
}
