//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"github.com/umayr/dill/internal/coordination"
	"os"
	"path/filepath"
	"testing"
)

func TestBrokerProfileInfoPrivateFileNoEngineContact(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("operator profile requires namespace UID0")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "profile.json")
	p := nativePodmanProfile()
	p.Podman.Socket = "/must-not-contact/engine.sock"
	raw, _ := json.Marshal(p)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = runBrokerProfileInfo([]string{"--contract", coordination.BrokerProfileVersion, "--broker-profile", path}, &out); err != nil {
		t.Fatal(err)
	}
	var info brokerProfileInfo
	if json.Unmarshal(out.Bytes(), &info) != nil || info.RuntimeOutcome != "not-run" || info.TargetDigest == "" {
		t.Fatal("offline export unavailable")
	}
}
