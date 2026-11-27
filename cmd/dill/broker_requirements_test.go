package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
)

func TestBrokerRequirementsNoEngineOrValues(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Secrets: []config.SecretRequirement{{Name: "password"}}, Services: map[string]*config.Service{"api": {Image: "fixture", Environment: map[string]string{"PRIVATE": "not-provider-output-do-not-return"}, Secrets: []config.SecretGrant{{Name: "password", Target: "/run/secrets/password", Mode: 0400}}}}}
	var out bytes.Buffer
	err := brokerRequirementsWith(context.Background(), []string{"--contract", brokersecret.Version, "--f=/captured/compose.pkl", "-p", "app"}, &out, func(context.Context, string) (*config.DillConfig, error) { return cfg, nil })
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
	base := []string{"--contract", brokersecret.Version, "-f", "/source"}
	for _, tc := range []struct {
		name string
		args []string
		load bool
	}{
		{"unknown flag", append(base, "-p", "app", "--secret-target", "old"), false},
		{"duplicate contract", append(base, "-p", "app", "--contract", brokersecret.Version), false},
		{"duplicate alias after empty", append(base, "-p=", "--p=app"), false},
		{"positional", append(base, "-p", "app", "extra"), false},
		{"trailing after terminator", append(base, "-p", "app", "--", "extra"), false},
		{"missing value", append(base, "-p"), false},
		{"missing stack", base, false},
		{"empty contract", []string{"--contract=", "-f", "/source", "-p", "app"}, false},
		{"empty config path", []string{"--contract", brokersecret.Version, "-f=", "-p", "app"}, false},
		{"relative config path", []string{"--contract", brokersecret.Version, "-f", "relative", "-p", "app"}, false},
		{"noncanonical config path", []string{"--contract", brokersecret.Version, "-f", "/dir/../source", "-p", "app"}, false},
		{"explicitly empty stack reaches loader", append(base, "-p="), true},
		{"valid flags reach loader", append(base, "-p", "app"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			called := false
			err := brokerRequirementsWith(context.Background(), tc.args, &out, func(_ context.Context, path string) (*config.DillConfig, error) {
				called = true
				if path != "/source" {
					t.Errorf("loader path = %q, want /source", path)
				}
				return nil, errors.New("private-evaluator-diagnostics")
			})
			if called != tc.load || err != (protocolExit{1}) {
				t.Fatalf("loader called = %t (want %t), exit = %v (want 1)", called, tc.load, err)
			}
			want := `{"Contract":"` + brokersecret.Version + `","Status":"unavailable","Error":"Secret requirements unavailable."}` + "\n"
			if out.String() != want {
				t.Fatal("unexpected or unsanitized failure reply")
			}
		})
	}
}
