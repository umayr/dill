//go:build linux

package coordination

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBrokerProfilePrivateFileAdmission(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("namespace-bound executor requires UID0")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "profile.json")
	p := BrokerProfile{Contract: BrokerProfileVersion, Docker: &Qualification{Layout: NativeDockerLayout, Socket: "/private/docker.sock", DaemonID: testDaemon}}
	raw, _ := json.Marshal(p)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadBrokerProfile(path); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadBrokerProfile(alias); err == nil {
		t.Fatal("symlink profile accepted")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadBrokerProfile(path); err == nil {
		t.Fatal("nonprivate profile accepted")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hardlink")
	if err = os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadBrokerProfile(path); err == nil {
		t.Fatal("multiply linked profile accepted")
	}
}
