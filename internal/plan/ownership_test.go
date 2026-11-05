package plan

import (
	"context"
	"errors"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type ownershipEngine struct {
	orchestrator.Engine
	live         *orchestrator.LiveConfig
	inspectErr   error
	inspected    string
	imageLookups int
}

func (e *ownershipEngine) InspectConfig(_ context.Context, name string) (*orchestrator.LiveConfig, error) {
	e.inspected = name
	return e.live, e.inspectErr
}

func (e *ownershipEngine) LocalImageID(context.Context, string) (string, error) {
	e.imageLookups++
	return "", nil
}

func (e *ownershipEngine) ListStack(context.Context, string) ([]string, error) { return nil, nil }

func TestComputeChecksOwnership(t *testing.T) {
	for _, customName := range []string{"", "shared-web"} {
		for _, tc := range []struct {
			name   string
			labels map[string]string
		}{
			{"unmanaged", nil},
			{"other stack", map[string]string{"dill.managed": "true", "dill.stack": "other", "dill.service": "web"}},
			{"other service", map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "db"}},
		} {
			t.Run(customName+"/"+tc.name, func(t *testing.T) {
				engine := &ownershipEngine{live: &orchestrator.LiveConfig{Image: "busybox", SystemLabels: tc.labels}}
				cfg := &config.DillConfig{Services: map[string]*config.Service{"web": {Image: "busybox", ContainerName: customName}}}
				p, err := Compute(context.Background(), cfg, engine, "app")
				if p != nil || !errors.Is(err, orchestrator.ErrOwnershipConflict) {
					t.Fatalf("Compute = %v, %v; want ownership conflict", p, err)
				}
				if engine.imageLookups != 0 {
					t.Fatal("resolved image before rejecting foreign ownership")
				}
				wantName := customName
				if wantName == "" {
					wantName = "app_web"
				}
				if engine.inspected != wantName {
					t.Fatalf("inspected %q, want %q", engine.inspected, wantName)
				}
			})
		}
	}
}

func TestComputeOwnedAndAbsentContainers(t *testing.T) {
	for _, tc := range []struct {
		name string
		live *orchestrator.LiveConfig
		err  error
		want ChangeKind
	}{
		{"absent", nil, orchestrator.ErrNotFound, KindCreate},
		{"matching", &orchestrator.LiveConfig{Image: "busybox", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}, nil, KindNoop},
		{"changed", &orchestrator.LiveConfig{Image: "old-image", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}, nil, KindRecreate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &ownershipEngine{live: tc.live, inspectErr: tc.err}
			cfg := &config.DillConfig{Services: map[string]*config.Service{"web": {Image: "busybox"}}}
			p, err := Compute(context.Background(), cfg, engine, "app")
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Changes) != 1 || p.Changes[0].Kind != tc.want {
				t.Fatalf("changes = %+v, want %v", p.Changes, tc.want)
			}
		})
	}
}
