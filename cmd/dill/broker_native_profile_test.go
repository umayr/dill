package main

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/preflight"
	"testing"
)

func nativePodmanProfile() *coordination.BrokerProfile {
	return &coordination.BrokerProfile{Contract: coordination.BrokerProfileVersion, Podman: &coordination.PodmanQualification{Layout: coordination.NativePodmanLayout, Socket: "/private/podman.sock", MachineID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Hostname: "synthetic-host", GraphRoot: "/private/storage", Namespace: coordination.NamespaceProfile{UIDMap: []coordination.IDMap{{Inside: 0, Outside: 0, Length: 4294967295}}, GIDMap: []coordination.IDMap{{Inside: 0, Outside: 0, Length: 4294967295}}}}}
}
func TestBrokerPrivateProfileArgsAndNoOrdinaryFallback(t *testing.T) {
	p := nativePodmanProfile()
	o := brokerCLIOptions{file: "/capture/compose.pkl", stack: "app", socket: p.Socket(), state: "/private/state", profile: p}
	args := o.nativeArgs(false, "")
	if _, reason := preflight.ParseBrokerInvocation(args, false); reason != "" {
		t.Fatal(reason)
	}
	if _, reason := preflight.ParseInvocation(args, false); reason == "" {
		t.Fatal("broker arguments authorized ordinary review")
	}
	opts := commandOptions{Engine: "podman", EngineSocket: p.Socket()}
	if !brokerProfileOptions(p, opts, p.Podman.Layout, "") {
		t.Fatal("exact operator profile refused")
	}
	for _, change := range []func(*commandOptions){func(o *commandOptions) { o.Engine = "docker" }, func(o *commandOptions) { o.EngineSocket = "/foreign.sock" }} {
		wrong := opts
		change(&wrong)
		if brokerProfileOptions(p, wrong, p.Podman.Layout, "") {
			t.Fatal("retarget accepted")
		}
	}
	c := &operationCoordination{options: coordinationOptions{profile: p, layout: p.Podman.Layout}}
	if c.prepare(context.Background(), opts, &config.DillConfig{Engine: "podman", Services: map[string]*config.Service{}}, "up", "never") == nil {
		t.Fatal("ordinary coordination acquired private broker profile")
	}
	if _, err := scopedBrokerProfile(nil, []*coordination.BrokerProfile{p}); err == nil {
		t.Fatal("profile alone enabled execution")
	}
	if _, err := scopedBrokerProfile(&brokersecret.Materialized{}, []*coordination.BrokerProfile{p}); err == nil {
		t.Fatal("unbound profile accepted")
	}
	if _, err := scopedBrokerProfile(nil, []*coordination.BrokerProfile{p, p}); err == nil {
		t.Fatal("ambiguous profiles accepted")
	}
}
func TestBrokerCLIProfileFlagIsExclusiveAndStrict(t *testing.T) {
	old := brokerTestArgs()
	args := []string{}
	for n := 0; n < len(old); n++ {
		if old[n] == "--engine-socket" || old[n] == "--coordination-daemon-id" {
			n++
			continue
		}
		args = append(args, old[n])
	}
	args = append(args, "--broker-profile", "/private/profile.json")
	o, err := parseBrokerCLI("broker-preflight", args)
	if err != nil || o.profilePath != "/private/profile.json" {
		t.Fatal("profile syntax refused", err)
	}
	for _, more := range [][]string{{"--engine-socket", "/other.sock"}, {"--coordination-daemon-id", "other-daemon"}, {"--broker-profile", "/other.json"}, {"--engine", "podman"}, {"--value", "not-accepted"}} {
		if _, err = parseBrokerCLI("broker-preflight", append(append([]string{}, args...), more...)); err == nil {
			t.Fatal("profile override accepted")
		}
	}
}
