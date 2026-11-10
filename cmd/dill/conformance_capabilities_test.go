package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// Discovery is static adapter support, not evidence of host/runtime readiness.
func TestConformanceCapabilityDiscoveryAndSelection(t *testing.T) {
	cases := []struct {
		name, message  string
		svc            *config.Service
		docker, podman bool
	}{
		{"device", "device mappings", &config.Service{Image: "synthetic:never", PullPolicy: "always", Devices: []string{"/dev/null:/dev/null"}}, true, false},
		{"namespace", "network mode", &config.Service{Image: "synthetic:never", PullPolicy: "always", NetworkMode: "container:other"}, true, false},
		{"unpublished", "unpublished port", &config.Service{Image: "synthetic:never", PullPolicy: "always", Ports: []json.RawMessage{json.RawMessage(`"80"`)}}, true, false},
	}
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			caps, err := orchestrator.DiscoverCapabilities(engine)
			if err != nil || caps.Engine != engine {
				t.Fatalf("discovery = %+v, %v", caps, err)
			}
			if caps.Devices != (engine == "docker") || caps.UnpublishedPorts != (engine == "docker") {
				t.Fatalf("discovery = %+v", caps)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					want := tc.podman
					if engine == "docker" {
						want = tc.docker
					}
					err := orchestrator.ValidateCapabilities(engine, map[string]*config.Service{"selected": tc.svc})
					if (err == nil) != want {
						t.Fatalf("static validation = %v, supported=%v", err, want)
					}
					if want {
						return
					} // Static support does not imply deployment success.
					cfg := &config.DillConfig{Name: "synthetic", Engine: engine, Services: map[string]*config.Service{"selected": tc.svc, "unselected": {Image: "untouched"}}}
					for _, names := range [][]string{nil, {"selected"}} {
						opened := false
						err = runUpConfig(context.Background(), commandOptions{Quiet: true}, cfg, names, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) {
							opened = true
							return newFakeEngine(), nil
						})
						if err == nil || !strings.Contains(err.Error(), tc.message) || opened {
							t.Fatalf("selection %v: error=%v engine opened=%v", names, err, opened)
						}
					}
				})
			}
		})
	}
}
