package preflight

import (
	"encoding/json"
	"github.com/umayr/dill/internal/orchestrator"
	"strings"
	"testing"
)

func TestBrokerPodmanResourceLeavesRemainEngineSpecific(t *testing.T) {
	raw := map[string]any{"name": "synthetic", "id": strings.Repeat("a", 64), "driver": "bridge", "network_interface": "podman1", "subnets": []any{map[string]string{"subnet": "10.89.0.0/24", "gateway": "10.89.0.1"}}, "ipv6_enabled": false, "internal": false, "dns_enabled": false, "labels": map[string]string{"dill.managed": "true", "dill.stack": "synthetic"}, "ipam_options": map[string]string{"driver": "host-local"}}
	makeFact := func(v map[string]any) orchestrator.PreviewResource {
		b, _ := json.Marshal(v)
		return orchestrator.PreviewResource{Kind: "network", Name: "synthetic", ActualName: "synthetic", ID: strings.Repeat("a", 64), Exists: true, Settings: b}
	}
	fact := makeFact(raw)
	base, err := BrokerResourceDigest("podman", "synthetic", fact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NetworkDigest("synthetic", fact); err == nil {
		t.Fatal("ordinary Docker leaf widened")
	}
	if _, err = BrokerResourceDigest("docker", "synthetic", fact); err == nil {
		t.Fatal("cross-engine facts accepted")
	}
	for _, key := range []string{"driver", "internal", "ipv6_enabled", "dns_enabled", "ipam_options", "subnets", "labels", "network_interface"} {
		copy := map[string]any{}
		for k, v := range raw {
			copy[k] = v
		}
		delete(copy, key)
		if _, err = BrokerResourceDigest("podman", "synthetic", makeFact(copy)); err == nil {
			t.Fatal("missing actual evidence accepted", key)
		}
	}
	for key, value := range map[string]any{"driver": "macvlan", "labels": map[string]string{}, "options": map[string]string{"parent": "eth0"}, "routes": []any{map[string]string{"destination": "0.0.0.0/0"}}, "internal": nil, "network_dns_servers": []string{"8.8.8.8"}} {
		copy := map[string]any{}
		for k, v := range raw {
			copy[k] = v
		}
		copy[key] = value
		if _, err = BrokerResourceDigest("podman", "synthetic", makeFact(copy)); err == nil {
			t.Fatal("unsupported/unknown network accepted", key)
		}
	}
	raw["dns_enabled"] = true
	if changed, err := BrokerResourceDigest("podman", "synthetic", makeFact(raw)); err != nil || changed == base {
		t.Fatal("actual DNS setting not committed")
	}
	volume := orchestrator.PreviewResource{Kind: "volume", Name: "synthetic_data", ActualName: "synthetic_data", Exists: true, Settings: []byte(`{"Name":"synthetic_data","Driver":"local","Mountpoint":"/private/volume","CreatedAt":"2026-01-01T00:00:00Z","Options":{},"Labels":{"dill.managed":"true","dill.stack":"synthetic"}}`)}
	if _, err = BrokerResourceDigest("podman", "synthetic", volume); err != nil {
		t.Fatal(err)
	}
	if _, err = VolumeDigest("synthetic", volume); err == nil {
		t.Fatal("Podman volume inferred Docker Scope")
	}
}
