package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"testing"
)

func TestBrokerGuardRequiresQualifiedTargetAndPrivateGeneration(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Services: map[string]*config.Service{"api": {Image: "fixture", PullPolicy: "never"}}}
	m := &brokersecret.Materialized{}
	for _, target := range []*coordination.Target{nil, {}} {
		if _, err := NewBrokerDockerEngine(target, cfg, m); err == nil {
			t.Fatal("unqualified target accepted")
		}
	}
	if _, err := WithBrokerGuardrails(context.Background(), nil, cfg, "app", m); err == nil {
		t.Fatal("zero generation accepted")
	}
	if err := (&DockerEngine{}).EnforceBrokerResources(cfg, m); err == nil {
		t.Fatal("raw Docker acquired broker mode")
	}
	if err := (&PodmanEngine{}).EnforceBrokerResources(cfg, m); err != brokersecret.ErrUnsupported {
		t.Fatal("unqualified Podman fallback")
	}
}
func TestBrokerAndRetiredDisposableModesCannotMix(t *testing.T) {
	d := &DockerEngine{broker: true}
	if d.EnforceDisposableVolumes() == nil || d.disposable {
		t.Fatal("mixed Docker modes")
	}
	p := &PodmanEngine{broker: true}
	if p.EnforceDisposableVolumes() == nil || p.disposable {
		t.Fatal("mixed Podman modes")
	}
}
func TestReadOnlyBrokerCreateCannotDelegate(t *testing.T) {
	e := DenyMutations(nil)
	creator, ok := e.(BrokerCreator)
	if !ok {
		t.Fatal("missing explicit refusal")
	}
	if _, err := creator.StartServiceBroker(context.Background(), "api", nil, "app", nil, nil); err != ErrReadOnlyEngine {
		t.Fatal("read-only broker create escaped")
	}
}
