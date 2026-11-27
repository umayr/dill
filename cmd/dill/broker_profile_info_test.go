package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/coordination"
)

func TestBrokerProfileOfflineInfo(t *testing.T) {
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
}

func TestBrokerProfileInfoFlagBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-profile.json")
	base := []string{"--contract", coordination.BrokerProfileVersion}
	args := append(base, "--broker-profile", path)
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"valid flags reach profile read", args, 1},
		{"valid aliases and equals", []string{"-contract=" + coordination.BrokerProfileVersion, "-broker-profile=" + path}, 1},
		{"duplicate alias", append(args, "-broker-profile="+path), 2},
		{"unknown shorthand", append(args, "-v"), 2},
		{"value override", append(args, "--value", "synthetic-canary"), 2},
		{"engine override", append(args, "--engine", "podman"), 2},
		{"positional", append(args, "extra"), 2},
		{"trailing after terminator", append(args, "--", "extra"), 2},
		{"missing value", append(base, "--broker-profile"), 2},
		{"missing profile", base, 2},
		{"empty profile", append(base, "--broker-profile="), 2},
		{"relative profile", append(base, "--broker-profile", "relative.json"), 2},
		{"noncanonical profile", append(base, "--broker-profile=/operator/../profile.json"), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runBrokerProfileInfo(tc.args, &out); err != (protocolExit{tc.code}) {
				t.Fatalf("exit = %v, want %d", err, tc.code)
			}
			want := brokerProfileInfo{Contract: coordination.BrokerProfileVersion, Code: "INVALID_INPUT"}
			if tc.code == 1 {
				want.Code = "PROFILE_UNAVAILABLE"
			}
			var got brokerProfileInfo
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || got != want {
				t.Fatalf("reply = %+v, want %+v (decode error %v)", got, want, err)
			}
		})
	}
}
