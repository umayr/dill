package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/brokersecret"
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
	profile := append(brokerTestCommonArgs(), "--broker-profile", "/operator/profile.json")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"duplicate contract alias", append(args, "-contract="+brokersecret.Version)},
		{"duplicate file alias", append(args, "--f=/elsewhere")},
		{"duplicate store", append(args, "--broker-store=/elsewhere")},
		{"unknown shorthand", append(args, "-v")},
		{"secret policy override", append(args, "--secret-policy", "private")},
		{"engine override", append(args, "--engine", "podman")},
		{"pull override", append(args, "--pull", "always")},
		{"leading positional", append([]string{"up"}, args...)},
		{"trailing positional", append(args, "up")},
		{"positional after terminator", append(args, "--", "up")},
		{"missing value", append(brokerTestCommonArgs(), "--broker-profile")},
		{"missing daemon", append(brokerTestCommonArgs(), "--engine-socket", "/var/run/docker.sock")},
		{"missing target", brokerTestCommonArgs()},
		{"profile with socket", append(profile, "--engine-socket", "/var/run/docker.sock")},
		{"profile with daemon", append(profile, "--coordination-daemon-id", "daemon-test")},
		{"profile with empty socket", append(profile, "--engine-socket=")},
		{"profile with empty daemon", append(profile, "--coordination-daemon-id", "")},
		{"direct with empty profile", append(args, "--broker-profile=")},
		{"noncanonical profile", append(brokerTestCommonArgs(), "--broker-profile=/operator/../profile.json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseBrokerCLI("broker-preflight", tc.args); err != brokersecret.ErrInvalid {
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
	for _, args := range [][]string{{"up"}, {"down"}} {
		var out bytes.Buffer
		err := runOperationScoped(context.Background(), args, nil, &brokersecret.Materialized{}, nil, &out)
		var result operation.Result
		if err == nil || json.Unmarshal(out.Bytes(), &result) != nil || result.Effects != "none" || result.Target != nil {
			t.Fatal("unbound broker dispatch reached target")
		}
	}
}
