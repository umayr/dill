package brokersecret

import (
	"encoding/json"
	"github.com/umayr/dill/internal/config"
	"strings"
	"testing"
)

func TestDescribeBoundedMetadataAndConfigIdentity(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Secrets: []config.SecretRequirement{{Name: "password"}}, Services: map[string]*config.Service{"api": {Image: "fixture", Environment: map[string]string{"TOKEN": "synthetic-source-value"}, Secrets: []config.SecretGrant{{Name: "password", Target: "/run/secrets/password", Mode: 0400}}}}}
	first, err := Describe(cfg, "app")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "synthetic-source-value") {
		t.Fatal("configuration value leaked")
	}
	cfg.Services["api"].Image = "changed"
	next, err := Describe(cfg, "app")
	if err != nil || first.ConfigDigest == next.ConfigDigest {
		t.Fatal("config identity not bound")
	}
	cfg.Secrets = append(cfg.Secrets, config.SecretRequirement{Name: "unused"})
	if _, err := Describe(cfg, "app"); err == nil {
		t.Fatal("unused secret may be acquired")
	}
}
