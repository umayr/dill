package main

import (
	"encoding/json"
	"github.com/umayr/dill/internal/coordination"
	"strings"
	"testing"
)

func TestBrokerProfileOfflineInfoAndStrictFlags(t *testing.T) {
	p := nativePodmanProfile()
	info, err := describeBrokerProfile(*p)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := p.TargetDigest()
	if info.TargetDigest != target || info.Engine != "podman" || info.RuntimeOutcome != "not-run" || info.EndpointDigest == "" {
		t.Fatal("invalid offline identity")
	}
	raw, _ := json.Marshal(info)
	for _, private := range []string{p.Podman.Hostname, p.Podman.GraphRoot, p.Socket(), p.Podman.MachineID, "UIDMap"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private profile leaked")
		}
	}
	args := []string{"--contract", coordination.BrokerProfileVersion, "--broker-profile", "/operator/profile.json"}
	if _, err = parseBrokerProfileInfo(args); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"-v"}, {"--value", "synthetic-canary"}, {"--broker-profile", "/other.json"}, {"--engine", "podman"}} {
		if _, err = parseBrokerProfileInfo(append(append([]string{}, args...), extra...)); err == nil {
			t.Fatal("unexpected flags accepted")
		}
	}
}
