package core_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/core"
)

type deployFake struct {
	core.DeployEngine
	mu                    sync.Mutex
	calls                 []string
	foreign, fail, cancel bool
}

func (f *deployFake) record(s string) { f.mu.Lock(); defer f.mu.Unlock(); f.calls = append(f.calls, s) }
func (f *deployFake) InspectConfig(ctx context.Context, n string) (*core.LiveConfig, error) {
	f.record("inspect:" + n)
	if f.foreign {
		return &core.LiveConfig{ID: "foreign"}, nil
	}
	return nil, core.ErrNotFound
}
func (f *deployFake) ListStack(context.Context, string) ([]string, error) {
	f.record("list")
	return nil, nil
}
func (f *deployFake) PullImage(ctx context.Context, image string, _ io.Writer) error {
	f.record("pull:" + image)
	if f.cancel {
		return context.Canceled
	}
	return nil
}
func (f *deployFake) EnsureNetwork(context.Context, string) error       { f.record("network"); return nil }
func (f *deployFake) ImageExists(context.Context, string) (bool, error) { return true, nil }
func (f *deployFake) StartService(ctx context.Context, n string, _ *core.Service, _ string) (string, error) {
	f.record("start:" + n)
	if f.fail {
		return "", errors.New("start failed")
	}
	return "id", nil
}
func (f *deployFake) IsReady(context.Context, string, bool) (bool, error) { return true, nil }
func (f *deployFake) Close() error                                        { f.record("close"); return nil }
func deployConfig() *core.Config {
	return &core.Config{Name: "stack", Engine: "docker", Services: map[string]*core.Service{"web": {Image: "web", PullPolicy: "always"}}}
}
func TestDeployExternalSuccessAndPreflight(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			f := &deployFake{}
			cfg := deployConfig()
			cfg.Engine = engine
			err := core.Deploy(context.Background(), cfg, core.DeployOptions{Stack: "stack", Timeout: time.Second, Open: func(context.Context, *core.Config) (core.DeployEngine, error) { return f, nil }})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"inspect:stack_web", "list", "pull:web", "inspect:stack_web", "list", "network", "start:web", "close"}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatalf("got %v want %v", f.calls, want)
			}
			if cfg.Services["web"].PullPolicy != "always" {
				t.Fatal("caller config modified")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*core.Config)
	}{
		{"secret", func(c *core.Config) { c.Services["web"].Secrets = []core.SecretGrant{{Name: "secret"}} }},
		{"cycle", func(c *core.Config) { c.Services["web"].DependsOn = []string{"web"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := deployConfig()
			tc.mutate(cfg)
			opened := false
			err := core.Deploy(context.Background(), cfg, core.DeployOptions{Stack: "stack", Open: func(context.Context, *core.Config) (core.DeployEngine, error) {
				opened = true
				return &deployFake{}, nil
			}})
			if err == nil || opened {
				t.Fatalf("err=%v opened=%v", err, opened)
			}
			if tc.name == "secret" && !errors.Is(err, core.ErrSecretDeployUnsupported) {
				t.Fatal(err)
			}
		})
	}
}
func TestDeployCanceledBeforeOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := false
	err := core.Deploy(ctx, deployConfig(), core.DeployOptions{Stack: "stack", Open: func(context.Context, *core.Config) (core.DeployEngine, error) {
		opened = true
		return &deployFake{}, nil
	}})
	if !errors.Is(err, context.Canceled) || opened {
		t.Fatalf("err=%v opened=%v", err, opened)
	}
}

func TestDeployOwnershipFailureAndPartialErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake *deployFake
		want string
	}{{"ownership", &deployFake{foreign: true}, "inspect:stack_web"}, {"partial", &deployFake{fail: true}, "start:web"}, {"cancel", &deployFake{cancel: true}, "pull:web"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fake
			err := core.Deploy(context.Background(), deployConfig(), core.DeployOptions{Stack: "stack", Timeout: time.Second, Open: func(context.Context, *core.Config) (core.DeployEngine, error) { return f, nil }})
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			found := false
			for _, c := range f.calls {
				if c == tc.want {
					found = true
				}
				if tc.name == "ownership" && (c == "pull:web" || c == "network") {
					t.Fatalf("mutation before ownership: %v", f.calls)
				}
			}
			if !found {
				t.Fatalf("missing %s: %v", tc.want, f.calls)
			}
		})
	}
}
