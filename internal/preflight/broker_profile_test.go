package preflight

import (
	"github.com/umayr/dill/internal/brokersecret"
	"strings"
	"testing"
)

func TestBrokerInvocationAdditionalProfilesDoNotWidenOrdinary(t *testing.T) {
	for _, tc := range []struct{ engine, layout, daemon string }{{"docker", "native-local-docker-v1", "synthetic-daemon"}, {"docker", "native-local-docker-userns-v1", "synthetic-daemon"}, {"podman", "native-local-podman-v1", ""}, {"podman", "native-local-podman-userns-v1", ""}} {
		args := []string{"up", "--contract", Version, "-f", "/capture/compose.pkl", "-p", "app", "--engine", tc.engine, "--engine-socket", "/private/engine.sock", "--coordination-layout", tc.layout, "--coordination-state-dir", "/private/state", "--coordination-source", "brine", "--pull", "never", "--no-prune"}
		if tc.daemon != "" {
			args = append(args, "--coordination-daemon-id", tc.daemon)
		}
		if _, reason := ParseBrokerInvocation(args, false); reason != "" {
			t.Fatal(tc, reason)
		}
		if tc.layout != "native-local-docker-v1" {
			if _, reason := ParseInvocation(args, false); reason == "" {
				t.Fatal("ordinary parser accepted broker-only profile")
			}
		}
		noPrune := append([]string(nil), args[:len(args)]...)
		for n, s := range noPrune {
			if s == "--no-prune" {
				noPrune[n] = "--no-prune=false"
			}
		}
		if _, reason := ParseBrokerInvocation(noPrune, false); reason == "" {
			t.Fatal("broker profile allowed prune")
		}
		if tc.engine == "podman" {
			if _, reason := ParseBrokerInvocation(append(args, "--coordination-daemon-id", "invented-uuid"), false); reason == "" {
				t.Fatal("Podman invented daemon identity accepted")
			}
		}
	}
}
func TestPodmanBrokerCanonicalDomainCannotAuthorizeOrdinary(t *testing.T) {
	input := sampleInput(t)
	input.Flags.NoPrune = true
	input.Engine = "podman"
	if _, err := input.Digest(); err == nil {
		t.Fatal("ordinary input accepted Podman")
	}
	a, err := input.BrokerDigest()
	if err != nil {
		t.Fatal(err)
	}
	input.Engine = "docker"
	b, err := input.BrokerDigest()
	if err != nil || a == b {
		t.Fatal("engine not committed")
	}
	ordinary, err := input.Digest()
	if err != nil || ordinary == b {
		t.Fatal("broker input domain collapsed")
	}
	n := sampleSelection(t)
	n.Target.Engine = "podman"
	if _, err = n.Digest(); err == nil {
		t.Fatal("ordinary selection accepted Podman")
	}
	d := "sha256:" + strings.Repeat("a", 64)
	scope := brokersecret.Scope{Registration: strings.Repeat("b", 32), Stack: n.Stack, Engine: "podman", TargetDigest: n.Target.QualifiedIdentityDigest, EndpointDigest: n.Target.EndpointDigest, SourceDigest: d, ConfigDigest: d, PolicyRevision: d, Generation: strings.Repeat("c", 32), BindingRevision: 1}
	selected := BrokerSelection{Contract: brokersecret.Version, Scope: scope, Handle: strings.Repeat("d", 32), Named: n}
	if _, err = selected.Digest(); err != nil {
		t.Fatal(err)
	}
	selected.Named.Target.Engine = "docker"
	if _, err = selected.Digest(); err == nil {
		t.Fatal("cross-engine broker selection accepted")
	}
}
