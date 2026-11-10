package orchestrator

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/umayr/dill/internal/config"
)

// Adapter preflight is static: it neither contacts a host nor establishes that
// controllers, host CPUs, privileges, or resource enforcement are available.
func TestResourceIntentStaticCapabilities(t *testing.T) {
	shares, pids := 512, 100
	svc := &config.Service{Image: "busybox", MemLimit: "128m", MemReservation: "32m", CPUs: "0.123456789", CPUShares: &shares, PidsLimit: &pids, Cpuset: "0-1"}
	want := config.ResourceLimits{Memory: 128 << 20, MemoryReservation: 32 << 20, NanoCPUs: 123450000, CPUShares: 512, PidsLimit: 100}
	got, err := config.NormalizeResources(svc)
	if err != nil || got != want {
		t.Fatalf("normalized = %+v, error = %v", got, err)
	}
	if err := config.ValidateRuntime(&config.DillConfig{Services: map[string]*config.Service{"app": svc}}); err != nil {
		t.Fatalf("runtime validation: %v", err)
	}
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			caps, err := DiscoverCapabilities(engine)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(caps)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			// An intentionally narrow snapshot: no resource/host/cgroup support
			// is represented or implied by a successful preflight.
			keys := map[string]bool{}
			for key := range fields {
				keys[key] = true
			}
			if !reflect.DeepEqual(keys, map[string]bool{"engine": true, "devices": true, "unpublishedPorts": true, "networkModes": true}) {
				t.Fatalf("unexpected capability claims: %s", data)
			}
			if err := ValidateCapabilities(engine, map[string]*config.Service{"app": svc}); err != nil {
				t.Fatalf("static adapter preflight rejected resource intent: %v", err)
			}
		})
	}
}
