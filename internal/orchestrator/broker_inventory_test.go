package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"testing"
)

type brokerListFixture struct {
	listed    []string
	inspected bool
}

func (f *brokerListFixture) ListStack(context.Context, string) ([]string, error) {
	return f.listed, nil
}
func (f *brokerListFixture) InspectConfig(context.Context, string) (*LiveConfig, error) {
	f.inspected = true
	return nil, nil
}
func TestBrokerInventoryRejectsZeroEvidence(t *testing.T) {
	cfg := &config.DillConfig{Engine: "docker", Services: map[string]*config.Service{"web": {Image: "alpine"}}}
	f := &brokerListFixture{listed: []string{"demo_omitted"}}
	for _, m := range []*brokersecret.Materialized{nil, {}} {
		if CheckBrokerInventory(context.Background(), f, cfg, "demo", m) == nil {
			t.Fatal("zero evidence inventory accepted")
		}
	}
	if f.inspected {
		t.Fatal("contacted workload without scope evidence")
	}
}
func TestBrokerEmptyGenerationLabelsRemainProtected(t *testing.T) {
	labels := map[string]string{}
	r := brokersecret.Review{Handle: "handle", Scope: brokersecret.Scope{Registration: "registration", Generation: "generation"}}
	ctx := context.WithValue(context.Background(), brokerCreateKey{}, r)
	applyBrokerLabels(ctx, labels)
	if labels[SecretEnabledLabel] != "true" || labels[BrokerHandleLabel] != "handle" {
		t.Fatal("empty broker workload lost protection")
	}
	ordinary := map[string]string{}
	applyBrokerLabels(context.Background(), ordinary)
	if len(ordinary) != 0 {
		t.Fatal("ordinary path acquired broker authority")
	}
}
