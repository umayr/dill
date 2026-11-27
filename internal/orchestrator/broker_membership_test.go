package orchestrator

import (
	"github.com/umayr/dill/internal/config"
	"testing"
)

func TestBrokerMembershipNoPruneCoverage(t *testing.T) {
	cfg := &config.DillConfig{Services: map[string]*config.Service{"web": {Image: "alpine"}}}
	for _, listed := range [][]string{{"demo_web", "demo_omitted"}, {"demo_web", "demo_web"}, {"demo_omitted"}} {
		if _, err := brokerMembership(cfg, "demo", listed); err == nil {
			t.Fatal("incomplete/duplicate/orphan inventory accepted")
		}
	}
	for _, listed := range [][]string{nil, {"demo_web"}} {
		if _, err := brokerMembership(cfg, "demo", listed); err != nil {
			t.Fatal("safe absent/retained membership rejected", err)
		}
	}
	cfg.Services["other"] = &config.Service{ContainerName: "demo_web"}
	if _, err := brokerMembership(cfg, "demo", nil); err == nil {
		t.Fatal("colliding desired workload accepted")
	}
}
