package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type statusCoreEngine struct{ orchestrator.Engine }

func (*statusCoreEngine) Close() error { return nil }
func (*statusCoreEngine) ListStack(context.Context, string) ([]string, error) {
	return []string{"a", "b"}, nil
}
func (*statusCoreEngine) ServiceStatus(_ context.Context, name string) (*orchestrator.ContainerStatus, error) {
	if name == "b" {
		return nil, errors.New("gone")
	}
	return &orchestrator.ContainerStatus{Name: name, State: "running"}, nil
}
func TestStatusCoreCLIJSONRegression(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return &statusCoreEngine{}, nil }
	out, _, err := captureCommand(t, "", func() error { return run([]string{"status", "--format", "json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Containers []orchestrator.ContainerStatus `json:"containers"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Containers) != 2 || got.Containers[0].State != "running" || got.Containers[1].State != "error" || got.Containers[1].Status != "gone" {
		t.Fatalf("unexpected JSON: %s", out)
	}
}
