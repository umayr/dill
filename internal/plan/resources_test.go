package plan

import (
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"testing"
)

func TestDiffConfigsResourceDrift(t *testing.T) {
	shares, pids := 512, 100
	svc := &config.Service{Image: "app:latest", MemLimit: "512m", MemReservation: "128m", CPUs: "0.123456789", CPUShares: &shares, PidsLimit: &pids}
	hash, err := config.ServiceConfigHash("stack", "app", svc)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := config.NormalizeResources(svc)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		alter func(*config.ResourceLimits)
	}{
		{"memLimit", func(r *config.ResourceLimits) { r.Memory *= 2 }},
		{"memReservation", func(r *config.ResourceLimits) { r.MemoryReservation *= 2 }},
		{"cpus", func(r *config.ResourceLimits) { r.NanoCPUs *= 2 }},
		{"cpuShares", func(r *config.ResourceLimits) { r.CPUShares *= 2 }},
		{"pidsLimit", func(r *config.ResourceLimits) { r.PidsLimit *= 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := &orchestrator.LiveConfig{Image: svc.Image, Resources: resources, SystemLabels: map[string]string{"dill.config-hash": hash}}
			tc.alter(&live.Resources)
			diffs, err := diffConfigs("app", svc, live, "", "stack")
			if err != nil {
				t.Fatal(err)
			}
			if len(diffs) != 1 || diffs[0].Field != tc.name || diffs[0].Before == diffs[0].After {
				t.Fatalf("expected %s drift with matching hash; got %+v", tc.name, diffs)
			}
		})
	}
	live := &orchestrator.LiveConfig{Image: svc.Image, Resources: resources}
	if diffs, err := diffConfigs("app", svc, live, "", "stack"); err != nil || len(diffs) != 0 {
		t.Fatalf("matching normalized resources: %+v, %v", diffs, err)
	}
}

func TestFormatResourceValue(t *testing.T) {
	for _, tc := range []struct {
		field string
		value int64
		want  string
	}{
		{"cpus", 0, "0"},
		{"cpus", 500000000, "0.5"},
		{"cpus", 123450000, "0.12345"},
		{"cpus", 2000000000, "2"},
		{"memLimit", 536870912, "536870912"},
		{"pidsLimit", -1, "-1"},
	} {
		if got := formatResourceValue(tc.field, tc.value); got != tc.want {
			t.Errorf("%s %d: got %q, want %q", tc.field, tc.value, got, tc.want)
		}
	}
}

func TestDiffConfigsResourceDefaults(t *testing.T) {
	svc := &config.Service{Image: "app:latest"}
	live := &orchestrator.LiveConfig{Image: svc.Image, Resources: config.ResourceLimits{PidsLimit: 2048, CPUShares: 1024}}
	if diffs, err := diffConfigs("app", svc, live, "", "stack"); err != nil || len(diffs) != 0 {
		t.Fatalf("engine defaults should not drift: %+v, %v", diffs, err)
	}
	unlimited := -1
	svc.PidsLimit = &unlimited
	for _, pids := range []int64{0, -1, 2048} {
		live.Resources.PidsLimit = pids
		diffs, err := diffConfigs("app", svc, live, "", "stack")
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if pids > 0 {
			want = 1
		}
		if len(diffs) != want {
			t.Fatalf("unlimited desired vs %d live: %+v", pids, diffs)
		}
	}
}

func TestDiffConfigsResourceRemoval(t *testing.T) {
	old := &config.Service{Image: "app:latest", MemLimit: "512m"}
	hash, err := config.ServiceConfigHash("stack", "app", old)
	if err != nil {
		t.Fatal(err)
	}
	svc := &config.Service{Image: old.Image}
	live := &orchestrator.LiveConfig{Image: svc.Image, SystemLabels: map[string]string{"dill.config-hash": hash}, Resources: config.ResourceLimits{Memory: 512 << 20}}
	diffs, err := diffConfigs("app", svc, live, "", "stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Field != "config_hash" {
		t.Fatalf("removing a limit must recreate: %+v", diffs)
	}
}
