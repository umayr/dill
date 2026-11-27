package main

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"strings"
	"testing"
)

func brokerTestArgs() []string {
	return []string{"--contract", brokersecret.Version, "--broker-store", "/private/store", "-f", "/capture/compose.pkl", "-p", "app", "--engine-socket", "/var/run/docker.sock", "--coordination-daemon-id", "daemon-test", "--coordination-state-dir", "/private/coordination", "--source-digest", brokersecret.Digest([]byte("source"))}
}
func TestBrokerCLIArgumentBoundary(t *testing.T) {
	args := brokerTestArgs()
	if _, err := parseBrokerCLI("broker-preflight", args); err != nil {
		t.Fatal(err)
	}
	for _, more := range [][]string{{"--contract", brokersecret.Version}, {"-v"}, {"--secret-policy", "private"}, {"--engine", "podman"}, {"--pull", "always"}, {"--broker-store", "/elsewhere"}, {"--f", "relative"}, {"up"}, {"--broker-store=/elsewhere"}} {
		bad := append(append([]string{}, args...), more...)
		if _, err := parseBrokerCLI("broker-preflight", bad); err == nil {
			t.Fatal("unsupported argument accepted")
		}
	}
	for _, kind := range []string{"broker-prepare", "broker-preflight", "broker-managed-operation"} {
		var out bytes.Buffer
		err := runBrokerCLI(context.Background(), kind, args, strings.NewReader(`{"scope":{},"scope":{},"value":"do-not-print"}`), &out)
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
