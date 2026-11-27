//go:build linux || darwin

package coordination

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrokerOperatorProfileStrictAndIdentityBound(t *testing.T) {
	rootful := BrokerProfile{Contract: BrokerProfileVersion, Docker: &Qualification{Layout: NativeDockerLayout, Socket: "/private/docker.sock", DaemonID: testDaemon}}
	raw, err := json.Marshal(rootful)
	if err != nil {
		t.Fatal(err)
	}
	p, err := decodeBrokerProfile(raw)
	if err != nil || p.Engine() != "docker" {
		t.Fatal(err)
	}
	a, _ := p.TargetDigest()
	plain, _ := DockerIdentity(testDaemon)
	if a != "sha256:"+plain {
		t.Fatal("legacy enrollment identity changed")
	}
	p.Docker.Layout = NativeDockerNamespaceLayout
	n := rootlessProfile()
	p.Namespace = &n
	b, err := p.TargetDigest()
	if err != nil || a == b {
		t.Fatal("namespace identity not separated")
	}
	q := podmanQ(t)
	p = BrokerProfile{Contract: BrokerProfileVersion, Podman: &q}
	if !p.Valid() || p.Engine() != "podman" {
		t.Fatal("Podman profile refused")
	}
	for _, data := range []string{string(raw[:len(raw)-1]) + `,"unknown":true}`, strings.Replace(string(raw), `"contract":`, `"contract":"other","contract":`, 1), string(raw) + "{}", strings.Repeat(" ", 64<<10+1)} {
		if _, err = decodeBrokerProfile([]byte(data)); err == nil {
			t.Fatal("untrusted profile document admitted")
		}
	}
	p.Docker = rootful.Docker
	if p.Valid() {
		t.Fatal("ambiguous engines accepted")
	}
	p = BrokerProfile{Contract: BrokerProfileVersion, Docker: &Qualification{Layout: NativePodmanLayout, Socket: "/socket", DaemonID: testDaemon}}
	if p.Valid() {
		t.Fatal("cross-engine layout accepted")
	}
}
