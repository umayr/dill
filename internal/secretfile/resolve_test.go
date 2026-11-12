package secretfile

import (
	"context"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func selectionConfig() *config.DillConfig {
	return &config.DillConfig{Name: "demo", Services: map[string]*config.Service{
		"app": {DependsOn: []string{"db"}}, "db": {}, "other": {},
	}}
}
func TestSelectServices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		names  []string
		noDeps bool
		want   int
	}{
		{"all", nil, false, 3}, {"closure", []string{"app"}, false, 2}, {"no-deps", []string{"app"}, true, 1}, {"duplicate", []string{"app", "app"}, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectServices(selectionConfig(), tc.names, tc.noDeps)
			if err != nil || len(got) != tc.want {
				t.Fatalf("selection: %v %v", got, err)
			}
		})
	}
	for _, change := range []func(*config.DillConfig){
		func(c *config.DillConfig) { c.Services["other"].DependsOn = []string{"missing"} },
		func(c *config.DillConfig) { c.Services["other"].DependsOn = []string{"other"} },
		func(c *config.DillConfig) { c.Services["db"].DependsOn = []string{"app"} },
		func(c *config.DillConfig) { c.Services["other"] = nil },
	} {
		c := selectionConfig()
		change(c)
		if _, err := selectServices(c, []string{"app"}, true); err != config.ErrSecretUnavailable {
			t.Fatalf("invalid unselected graph: %v", err)
		}
	}
	if _, err := selectServices(selectionConfig(), []string{"sentinel"}, false); err != config.ErrSecretUnavailable {
		t.Fatal("unknown selection accepted")
	}
}
func TestPreflightFixedFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, req := range []Request{{}, {Config: selectionConfig(), PolicyPath: "/sentinel", BindingsPath: "/sentinel"}} {
		if got, err := Preflight(ctx, req); got != nil || err != config.ErrSecretUnavailable {
			t.Fatalf("fixed failure: %v %v", got, err)
		}
	}
	if _, err := Preflight(nil, Request{}); err != config.ErrSecretUnavailable {
		t.Fatal("nil context accepted")
	}
}
