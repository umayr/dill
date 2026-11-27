package agentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"strings"
	"testing"
)

func TestBrokerProfileRecoveryWithoutProfileOrRuntimeFiles(t *testing.T) {
	o := localBrokerTestOptions(t)
	o.socket = ""
	o.daemon = ""
	o.profile = "/missing/profile.json"
	o.engine = "podman"
	o.target = brokersecret.Digest([]byte("qualified-podman"))
	o.endpoint = brokersecret.Digest([]byte("unix:///missing/engine.sock"))
	parsed, parseErr := parseLocalBroker([]string{"journal", "--contract", brokersecret.Version, "--coordination-state-dir", o.state, "--broker-store", o.store, "--history-dir", o.history, "--staging-root", o.root, "--staging-catalog", o.catalog, "--pkl", o.pkl, "--broker-profile", o.profile, "--engine-kind", o.engine, "--target-digest", o.target, "--endpoint-digest", o.endpoint})
	if parseErr != nil || parsed != o {
		t.Fatalf("profile parser mismatch %v %#v %#v", parseErr, parsed, o)
	}
	target := brokersecret.Digest([]byte("dill-broker-local-profile/1\x00" + o.engine + "\x00" + o.target + "\x00" + o.endpoint))
	history, err := openDeploymentHistory(o.history, target)
	if err != nil {
		t.Fatal(err)
	}
	o.journal = history.journal
	history.dir.Close()
	if _, err = runLocalBroker(context.Background(), o, nil); err != nil {
		t.Fatal("offline journal failed", err)
	}
	o.action = "fence"
	id := strings.Repeat("1", 32)
	body, _ := json.Marshal(map[string]string{"requestId": id, "stack": "demo", "confirm": "fence-if-unadmitted"})
	if _, err = runLocalBroker(context.Background(), o, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	o.action = "status"
	o.id = id
	if r, err := runLocalBroker(context.Background(), o, nil); err != nil || !bytes.Contains(r.Body, []byte("not-admitted")) {
		t.Fatal("same-request recovery lost", err)
	}
	o.target = brokersecret.Digest([]byte("different"))
	if _, err = runLocalBroker(context.Background(), o, nil); err == nil {
		t.Fatal("retarget reused journal")
	}
}
func TestBrokerLocalProfileSyntaxAndChildArgv(t *testing.T) {
	args := []string{"journal", "--contract", brokersecret.Version, "--broker-profile", "/private/profile.json", "--engine-kind", "podman", "--target-digest", brokersecret.Digest([]byte("target")), "--endpoint-digest", brokersecret.Digest([]byte("endpoint")), "--coordination-state-dir", "/private/state", "--broker-store", "/private/runtime", "--history-dir", "/private/history", "--staging-root", "/private/stages", "--staging-catalog", "/private/catalog", "--pkl", "/operator/pkl"}
	if _, err := parseLocalBroker(args); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"--engine-socket", "/foreign.sock"}, {"--daemon-id", "foreign-daemon"}, {"--broker-profile", "/other.json"}, {"--value", "denied"}} {
		if _, err := parseLocalBroker(append(append([]string{}, args...), extra...)); err == nil {
			t.Fatal("override accepted")
		}
	}
	c := brokerCLI{profile: "/private/profile.json", socket: "/must-not-be-used", daemon: "must-not-be-used"}
	wire := strings.Join(c.args("broker-preflight", "/capture/compose.pkl", brokersecret.Scope{Stack: "demo"}), " ")
	if !strings.Contains(wire, "--broker-profile /private/profile.json") || strings.Contains(wire, "--engine-socket") || strings.Contains(wire, "--coordination-daemon-id") {
		t.Fatal("mixed child selection")
	}
}
func TestHTTPSBrokerProfileMustMatchDockerEnrollment(t *testing.T) {
	p := coordination.BrokerProfile{Contract: coordination.BrokerProfileVersion, Docker: &coordination.Qualification{Layout: coordination.NativeDockerLayout, Socket: "/run/docker.sock", DaemonID: "daemon-test"}}
	if _, _, err := httpsBrokerProfileScope(p, p.Socket(), "daemon-test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := httpsBrokerProfileScope(p, "/foreign.sock", "daemon-test"); err == nil {
		t.Fatal("HTTPS engine retarget")
	}
	if _, _, err := httpsBrokerProfileScope(p, p.Socket(), "foreign-daemon"); err == nil {
		t.Fatal("HTTPS daemon retarget")
	}
	p.Docker = nil
	p.Podman = &coordination.PodmanQualification{Layout: coordination.NativePodmanLayout, Socket: "/run/podman.sock", MachineID: strings.Repeat("a", 32), Hostname: "synthetic-host", GraphRoot: "/private/storage", Namespace: coordination.NamespaceProfile{UIDMap: []coordination.IDMap{{Inside: 0, Outside: 0, Length: 4294967295}}, GIDMap: []coordination.IDMap{{Inside: 0, Outside: 0, Length: 4294967295}}}}
	if !p.Valid() {
		t.Fatal("invalid test Podman qualification")
	}
	if _, _, err := httpsBrokerProfileScope(p, "/run/podman.sock", "daemon-test"); err == nil {
		t.Fatal("Podman silently reused Docker monitor")
	}
}
func TestBrokerEndpointEngineCeiling(t *testing.T) {
	_, ref, _ := brokerFixture(t)
	ref.Scope.Engine = "podman"
	h := &brokerEndpoint{target: ref.Scope.TargetDigest, endpoint: ref.Scope.EndpointDigest}
	if h.scopeOK(ref.Scope) {
		t.Fatal("legacy endpoint accepted other engine")
	}
	h.engine = "podman"
	if !h.scopeOK(ref.Scope) {
		t.Fatal("explicit Podman ceiling refused")
	}
}
