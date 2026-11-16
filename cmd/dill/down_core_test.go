package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type downCoreEngine struct {
	orchestrator.Engine
	calls []string
}

func (f *downCoreEngine) Close() error { return nil }
func (f *downCoreEngine) ListStack(_ context.Context, stack string) ([]string, error) {
	f.calls = append(f.calls, "list:"+stack)
	return []string{"one"}, nil
}
func (f *downCoreEngine) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	f.calls = append(f.calls, "inspect:"+name)
	return &orchestrator.LiveConfig{ID: "immutable", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "test-stack", "dill.service": "one"}}, nil
}
func (f *downCoreEngine) StopService(_ context.Context, id string) error {
	f.calls = append(f.calls, "stop:"+id)
	return errors.New("stop failure")
}
func (f *downCoreEngine) RemoveService(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove:"+id)
	return nil
}
func (f *downCoreEngine) RemoveNetwork(_ context.Context, name string) error {
	f.calls = append(f.calls, "network:"+name)
	return nil
}
func TestDownCoreCLIParity(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	cli := &downCoreEngine{}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return cli, nil }
	cliErr := runDown(context.Background(), commandOptions{StackName: "test-stack", Quiet: true}, nil)
	direct := &downCoreEngine{}
	_, coreErr := core.Down(context.Background(), direct, "test-stack", nil)
	if cliErr == nil || coreErr == nil || cliErr.Error() != coreErr.Error() || !reflect.DeepEqual(cli.calls, direct.calls) {
		t.Fatalf("CLI %v %v; core %v %v", cli.calls, cliErr, direct.calls, coreErr)
	}
}
