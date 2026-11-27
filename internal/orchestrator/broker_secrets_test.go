package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"testing"
)

func TestBrokerCreationRequiresOpaqueActiveEvidence(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Services: map[string]*config.Service{"api": {Image: "fixture"}}}
	svc := cfg.Services["api"]
	for _, m := range []*brokersecret.Materialized{nil, {}} {
		d := &DockerEngine{}
		if _, err := d.StartServiceBroker(context.Background(), "api", svc, "app", cfg, m); err == nil {
			t.Fatal("unqualified Docker creation")
		}
		p := &PodmanEngine{}
		if _, err := p.StartServiceBroker(context.Background(), "api", svc, "app", cfg, m); err == nil {
			t.Fatal("unqualified Podman creation")
		}
		if _, err := WithBrokerSecrets(d, cfg, m); err == nil {
			t.Fatal("unqualified engine wrapper")
		}
	}
}
func TestBrokerDoesNotEnableOrdinarySecretCreation(t *testing.T) {
	svc := &config.Service{Image: "fixture", Secrets: []config.SecretGrant{{Name: "password", Target: "/run/secrets/password", Mode: 0400}}}
	if _, err := (&DockerEngine{}).StartService(context.Background(), "api", svc, "app"); err != config.ErrSecretUnavailable {
		t.Fatal("ordinary Docker secret denial lost")
	}
	if _, err := (&PodmanEngine{}).StartService(context.Background(), "api", svc, "app"); err != config.ErrSecretUnavailable {
		t.Fatal("ordinary Podman secret denial lost")
	}
}
