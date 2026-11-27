package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func brokerTestCommonArgs() []string {
	return []string{"--contract", brokersecret.Version, "--broker-store", "/private/store", "-f", "/capture/compose.pkl", "-p", "app", "--coordination-state-dir", "/private/coordination", "--source-digest", brokersecret.Digest([]byte("source"))}
}

func brokerTestArgs() []string {
	return append(brokerTestCommonArgs(), "--engine-socket", "/var/run/docker.sock", "--coordination-daemon-id", "daemon-test")
}

func TestBrokerCLIValidFlags(t *testing.T) {
	o, err := parseBrokerCLI("broker-init", []string{"-contract=" + brokersecret.Version, "--broker-store=/private/store", "--"})
	if err != nil || o.store != "/private/store" {
		t.Fatalf("broker-init: store = %q, err = %v", o.store, err)
	}
	for _, kind := range []string{"broker-prepare", "broker-retirement-prepare", "broker-preflight", "broker-managed-operation"} {
		for _, tc := range []struct {
			name, profile, socket, daemon string
			args                          []string
		}{
			{name: "direct", socket: "/var/run/docker.sock", daemon: "daemon-test", args: brokerTestArgs()},
			{name: "profile", profile: "/operator/profile.json", args: append(brokerTestCommonArgs(), "--broker-profile=/operator/profile.json")},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				o, err := parseBrokerCLI(kind, tc.args)
				if err != nil || o.profilePath != tc.profile || o.socket != tc.socket || o.daemon != tc.daemon || o.stack != "app" || o.file != "/capture/compose.pkl" {
					t.Fatalf("options = %+v, err = %v", o, err)
				}
			})
		}
	}
}

func TestBrokerCLIArgumentBoundary(t *testing.T) {
	args := brokerTestArgs()
	common := brokerTestCommonArgs()
	profile := append(brokerTestCommonArgs(), "--broker-profile", "/operator/profile.json")
	cases := []struct {
		name         string
		base, suffix []string
	}{
		{"duplicate contract alias", args, []string{"-contract=" + brokersecret.Version}},
		{"duplicate file alias", args, []string{"--f=/elsewhere"}},
		{"duplicate store", args, []string{"--broker-store=/elsewhere"}},
		{"unknown shorthand", args, []string{"-v"}},
		{"secret policy override", args, []string{"--secret-policy", "private"}},
		{"engine override", args, []string{"--engine", "podman"}},
		{"pull override", args, []string{"--pull", "always"}},
		{"leading positional", []string{"up"}, args},
		{"trailing positional", args, []string{"up"}},
		{"positional after terminator", args, []string{"--", "up"}},
		{"missing value", common, []string{"--broker-profile"}},
		{"missing daemon", common, []string{"--engine-socket", "/var/run/docker.sock"}},
		{"missing target", common, nil},
		{"profile with socket", profile, []string{"--engine-socket", "/var/run/docker.sock"}},
		{"profile with daemon", profile, []string{"--coordination-daemon-id", "daemon-test"}},
		{"profile with empty socket", profile, []string{"--engine-socket="}},
		{"profile with empty daemon", profile, []string{"--coordination-daemon-id", ""}},
		{"direct with empty profile", args, []string{"--broker-profile="}},
		{"noncanonical profile", common, []string{"--broker-profile=/operator/../profile.json"}},
	}
	// Build every input before checking suffixes so later appends cannot silently
	// replace the invalid argument an earlier case is supposed to exercise.
	inputs := make([][]string, len(cases))
	for i, tc := range cases {
		inputs[i] = append(slices.Clone(tc.base), tc.suffix...)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inputs[i][len(tc.base):]; !slices.Equal(got, tc.suffix) {
				t.Fatalf("argument suffix = %q, want %q", got, tc.suffix)
			}
			if _, err := parseBrokerCLI("broker-preflight", inputs[i]); err != brokersecret.ErrInvalid {
				t.Fatalf("got %v, want invalid input", err)
			}
		})
	}
	if _, err := parseBrokerCLI("broker-init", args); err != brokersecret.ErrInvalid {
		t.Fatalf("broker-init with target flags: got %v, want invalid input", err)
	}
}

func TestBrokerCLIRejectsInvalidStdin(t *testing.T) {
	for _, kind := range []string{"broker-prepare", "broker-preflight", "broker-managed-operation"} {
		var out bytes.Buffer
		err := runBrokerCLI(context.Background(), kind, brokerTestArgs(), strings.NewReader(`{"scope":{},"scope":{},"value":"do-not-print"}`), &out)
		if err == nil || strings.Contains(out.String(), "do-not-print") || !strings.Contains(out.String(), "INVALID_INPUT") {
			t.Fatal("invalid stdin leaked or reached state")
		}
	}
}
func TestBrokerNativeInvocationIsBoundedAndIdentical(t *testing.T) {
	o, err := parseBrokerCLI("broker-preflight", brokerTestArgs())
	if err != nil {
		t.Fatal(err)
	}
	preview, reason := preflight.ParseInvocation(o.nativeArgs(false, ""), false)
	if reason != "" || preview.Pull != "never" || !preview.NoPrune || preview.Engine != "docker" {
		t.Fatal("invalid native preview")
	}
	digest := brokersecret.Digest([]byte("selection"))
	apply, reason := preflight.ParseInvocation(o.nativeArgs(true, digest), true)
	if reason != "" || apply.ExpectedPreflight != digest || apply.ConfigFile != preview.ConfigFile || apply.Stack != preview.Stack || apply.EffectiveFlags() != preview.EffectiveFlags() {
		t.Fatal("preview/apply contract differs")
	}
}
func TestBrokerManagedDispatchRequiresExplicitBoundOwnership(t *testing.T) {
	o, err := parseBrokerCLI("broker-managed-operation", brokerTestArgs())
	if err != nil {
		t.Fatal(err)
	}
	up := o.nativeArgs(true, brokersecret.Digest([]byte("selection")))
	if _, reason := preflight.ParseBrokerInvocation(up, true); reason != "" || !namedBindingIntent(up) || up[0] != "up" {
		t.Fatalf("expected a valid bound broker up invocation, parser reason: %q", reason)
	}
	cfg := &config.DillConfig{
		Name:   o.stack,
		Engine: "docker",
		Services: map[string]*config.Service{
			"app": {Image: "alpine:3.20", PullPolicy: "never", Restart: "no"},
		},
	}
	if err := config.ValidateRuntime(cfg); err != nil {
		t.Fatalf("invalid broker config fixture: %v", err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bound up without ownership", up},
		{"bare down", []string{"down"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runOperationScoped(context.Background(), tc.args, nil, &brokersecret.Materialized{}, cfg, &out); err == nil {
				t.Fatal("expected broker dispatch to fail")
			}
			var result operation.Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("decode operation result: %v", err)
			}
			want := operation.Error{Code: "INVALID_ARGUMENTS", Message: "Invalid broker operation."}
			if len(result.Errors) != 1 || result.Errors[0] != want {
				t.Fatalf("operation errors = %+v, want [%+v]", result.Errors, want)
			}
			if result.Outcome != "invalid_input" || result.Effects != "none" || result.Target != nil {
				t.Fatalf("expected invalid input with no effects or target, got %+v", result)
			}
		})
	}
}
