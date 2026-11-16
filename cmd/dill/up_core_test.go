package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestOrdinaryUpCoreParity(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			cfg := func() *config.DillConfig {
				return &config.DillConfig{Name: "test", Engine: engine, Services: map[string]*config.Service{"a": {Image: "a", PullPolicy: "always"}}}
			}
			cli := &recordingUpEngine{fakeEngine: newFakeEngine()}
			direct := &recordingUpEngine{fakeEngine: newFakeEngine()}
			err := runUpConfig(context.Background(), commandOptions{Quiet: true, Timeout: time.Second}, cfg(), nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return cli, nil })
			if err != nil {
				t.Fatal(err)
			}
			err = core.Deploy(context.Background(), cfg(), core.DeployOptions{Stack: "test", Timeout: time.Second, Open: func(context.Context, *core.Config) (core.DeployEngine, error) { return direct, nil }})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cli.events, direct.events) {
				t.Fatalf("cli=%v core=%v", cli.events, direct.events)
			}
		})
	}
}
