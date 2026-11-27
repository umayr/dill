package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"strings"
	"testing"
)

func TestBrokerRequirementsNoEngineOrValues(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Secrets: []config.SecretRequirement{{Name: "password"}}, Services: map[string]*config.Service{"api": {Image: "fixture", Environment: map[string]string{"PRIVATE": "not-provider-output-do-not-return"}, Secrets: []config.SecretGrant{{Name: "password", Target: "/run/secrets/password", Mode: 0400}}}}}
	var out bytes.Buffer
	err := brokerRequirementsWith(context.Background(), []string{"--contract", brokersecret.Version, "-f", "/captured/compose.pkl", "-p", "app"}, &out, func(context.Context, string) (*config.DillConfig, error) { return cfg, nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not-provider-output") || strings.Contains(out.String(), "/captured/") {
		t.Fatal("discovery returned evaluated values or host paths")
	}
	var desc brokersecret.Requirements
	if json.Unmarshal(out.Bytes(), &desc) != nil || len(desc.Names) != 1 || len(desc.Grants) != 1 || desc.ConfigDigest == "" {
		t.Fatal("invalid descriptor")
	}
	if config.RejectUncheckedSecretDelivery(cfg) == nil {
		t.Fatal("ordinary path lost denial")
	}
}
func TestBrokerRequirementsRefusesFlagsAndSanitizes(t *testing.T) {
	for _, args := range [][]string{{"--secret-target", "old"}, {"--contract", brokersecret.Version, "--contract", brokersecret.Version, "-f", "/source", "-p", "app"}, {"--contract", brokersecret.Version, "-f", "relative", "-p", "app"}} {
		var out bytes.Buffer
		called := false
		err := brokerRequirementsWith(context.Background(), args, &out, func(context.Context, string) (*config.DillConfig, error) { called = true; return nil, nil })
		if err == nil || called {
			t.Fatal("invalid invocation reached loader")
		}
	}
	var out bytes.Buffer
	err := brokerRequirementsWith(context.Background(), []string{"--contract", brokersecret.Version, "-f", "/source", "-p", "app"}, &out, func(context.Context, string) (*config.DillConfig, error) {
		return nil, errors.New("private-evaluator-diagnostics")
	})
	if err == nil || strings.Contains(out.String(), "private-evaluator") {
		t.Fatal("unsanitized failure")
	}
}
