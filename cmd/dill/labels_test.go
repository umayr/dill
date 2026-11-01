package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func withConfigLoader(t *testing.T, fn func(context.Context, string) (*config.DillConfig, error)) {
	t.Helper()
	old := configLoader
	configLoader = fn
	t.Cleanup(func() { configLoader = old })
}

func TestResolveLabelStackFallback(t *testing.T) {
	withConfigLoader(t, func(context.Context, string) (*config.DillConfig, error) {
		return nil, errors.New("broken config")
	})

	st, err := resolveLabelStack(context.Background(), commandOptions{StackName: "demo", Engine: "docker"})
	if err != nil {
		t.Fatalf("resolveLabelStack: %v", err)
	}
	if st.Config != nil {
		t.Error("expected nil config on fallback")
	}
	if st.Name != "demo" || st.Engine != "docker" {
		t.Errorf("stack = %+v, want demo/docker", st)
	}
}

func TestResolveLabelStackRequiresName(t *testing.T) {
	withConfigLoader(t, func(context.Context, string) (*config.DillConfig, error) {
		return nil, errors.New("broken config")
	})

	_, err := resolveLabelStack(context.Background(), commandOptions{})
	if err == nil {
		t.Fatal("expected an error without a stack name")
	}
	if !strings.Contains(err.Error(), "pass -p") {
		t.Fatalf("error does not mention -p: %v", err)
	}
}

func TestResolveLabelStackUsesConfig(t *testing.T) {
	withConfigLoader(t, func(context.Context, string) (*config.DillConfig, error) {
		return &config.DillConfig{Name: "app", Engine: "docker"}, nil
	})

	st, err := resolveLabelStack(context.Background(), commandOptions{})
	if err != nil {
		t.Fatalf("resolveLabelStack: %v", err)
	}
	if st.Config == nil || st.Name != "app" || st.Engine != "docker" {
		t.Fatalf("stack = %+v, want app/docker with config", st)
	}
}

func TestLabelStackRequireConfig(t *testing.T) {
	if err := (&labelStack{}).requireConfig("down"); err == nil {
		t.Fatal("expected an error for a command that needs the config")
	}
	if err := (&labelStack{Config: &config.DillConfig{}}).requireConfig("down"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
