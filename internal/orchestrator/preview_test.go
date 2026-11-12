package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	nettypes "github.com/containers/common/libnetwork/types"
)

func TestPreviewResourceStableWireParity(t *testing.T) {
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			volatile := false
			e := secretWireEngine(t, name, contextTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/networks/") {
					created := "2024-01-01T00:00:00Z"
					if volatile {
						created = "2025-01-01T00:00:00Z"
					}
					if name == "podman" {
						return contextResponse(r, 200, `{"id":"network-id","name":"demo","driver":"bridge","created":"`+created+`","labels":{"dill.stack":"demo"},"options":{}}`), nil
					}
					return contextResponse(r, 200, `{"Id":"network-id","Name":"demo","Driver":"bridge","Created":"`+created+`","Containers":{"irrelevant":{"Name":"dynamic"}},"Labels":{"dill.stack":"demo"},"Options":{}}`), nil
				}
				if strings.Contains(r.URL.Path, "/volumes/") {
					return contextResponse(r, 200, `{"Name":"data","Driver":"local","CreatedAt":"2024-01-01T00:00:00Z","Mountpoint":"/private/volume","Options":{},"Labels":{"dill.stack":"demo"},"MountCount":97,"UsageData":{"Size":123,"RefCount":7}}`), nil
				}
				t.Fatalf("unexpected read endpoint: %s", r.URL.Path)
				return nil, nil
			}))
			inspector := e.(PreviewResourceInspector)
			a, err := inspector.PreviewResources(context.Background(), "demo", []string{"data", "data"})
			if err != nil || len(a) != 2 || a[0].ID != "network-id" || !a[1].Exists {
				t.Fatalf("resource facts unavailable: %v", err)
			}
			volatile = true
			b, err := inspector.PreviewResources(context.Background(), "demo", []string{"data"})
			if err != nil {
				t.Fatal(err)
			}
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			if string(x) != string(y) {
				t.Fatal("irrelevant resource timestamp entered proof")
			}
			text := string(x)
			for _, field := range []string{"MountCount", "UsageData", "RefCount", "Containers", "irrelevant"} {
				if strings.Contains(text, field) {
					t.Fatal("volatile resource fields entered proof")
				}
			}
		})
	}
}

func TestPodmanPreviewNetworkConfigurationAndAttachments(t *testing.T) {
	// Exact SDK wire fields: NetworkInspectReport embeds Network and adds
	// containers.<ID>.interfaces.<name>.{subnets[].ipnet,mac_address}.
	// Network-level subnets/routes/DNS/IPAM remain approval inputs; endpoint
	// membership and assigned IP/MAC addresses are not network configuration.
	const configuration = `{
		"name":"demo","id":"network-id","driver":"bridge",
		"network_interface":"podman1","created":"2024-01-01T00:00:00Z",
		"subnets":[{"subnet":"10.42.0.0/24","gateway":"10.42.0.1","lease_range":{"start_ip":"10.42.0.10","end_ip":"10.42.0.200"}}],
		"routes":[{"destination":"192.0.2.0/24","gateway":"10.42.0.2","metric":10}],
		"ipv6_enabled":true,"internal":false,"dns_enabled":true,
		"network_dns_servers":["192.0.2.53","192.0.2.54"],
		"labels":{"dill.stack":"demo","custom":"retained"},
		"options":{"mtu":"1500"},"ipam_options":{"driver":"host-local"}
	}`
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(configuration), &report); err != nil {
		t.Fatal(err)
	}
	e := secretWireEngine(t, "podman", contextTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/networks/") {
			t.Fatal("unexpected endpoint for network-only observations")
		}
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		return contextResponse(r, 200, string(body)), nil
	}))
	snapshot := func() []byte {
		t.Helper()
		facts, err := e.(PreviewResourceInspector).PreviewResources(context.Background(), "demo", nil)
		if err != nil || len(facts) != 1 || !facts[0].Exists {
			t.Fatal("network facts unavailable")
		}
		data, err := json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	baseline := snapshot()
	// Compare against the complete SDK network type, not a hand-picked subset.
	// This prevents accidentally discarding static settings while fixing churn.
	var network nettypes.Network
	if err := json.Unmarshal([]byte(configuration), &network); err != nil {
		t.Fatal(err)
	}
	network.Created = time.Time{}
	settings, err := json.Marshal(network)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal([]PreviewResource{{Kind: "network", Name: "demo", ID: "network-id", Exists: true, Settings: settings}})
	if err != nil || !bytes.Equal(baseline, expected) {
		t.Fatal("complete network configuration or immutable identity not retained")
	}
	for _, tc := range []struct{ name, attachments string }{
		{"empty", `{}`},
		{"stopped", `null`},
		{"attached", `{"owned-id":{"name":"demo_app","interfaces":{"eth0":{"subnets":[{"ipnet":"10.42.0.10/24","gateway":"10.42.0.1"}],"mac_address":"02:42:0a:2a:00:0a"}}}}`},
		{"same container new addresses", `{"owned-id":{"name":"demo_app","interfaces":{"eth0":{"subnets":[{"ipnet":"10.42.0.11/24","gateway":"10.42.0.1"}],"mac_address":"02:42:0a:2a:00:0b"}}}}`},
		{"foreign endpoints and interfaces", `{"foreign-a":{"name":"other","interfaces":{"eth0":{"subnets":[{"ipnet":"10.42.0.12/24"}],"mac_address":"02:42:0a:2a:00:0c"},"eth1":{"subnets":[{"ipnet":"2001:db8::2/64"}],"mac_address":"02:42:00:00:00:02"}}},"foreign-b":{"name":"another","interfaces":{}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report["containers"] = json.RawMessage(tc.attachments)
			if !bytes.Equal(baseline, snapshot()) {
				t.Fatal("runtime endpoint census changed private network evidence")
			}
		})
	}
	delete(report, "containers")
	originalCreated := report["created"]
	report["created"] = json.RawMessage(`"2025-01-01T00:00:00Z"`)
	if !bytes.Equal(baseline, snapshot()) {
		t.Fatal("volatile network timestamp entered private evidence")
	}
	report["created"] = originalCreated
	for _, tc := range []struct{ name, field, value string }{
		{"identity", "id", `"replacement-id"`},
		{"name", "name", `"other"`},
		{"driver", "driver", `"macvlan"`},
		{"host interface", "network_interface", `"podman2"`},
		{"subnet", "subnets", `[{"subnet":"10.43.0.0/24","gateway":"10.42.0.1","lease_range":{"start_ip":"10.42.0.10","end_ip":"10.42.0.200"}}]`},
		{"gateway", "subnets", `[{"subnet":"10.42.0.0/24","gateway":"10.42.0.2","lease_range":{"start_ip":"10.42.0.10","end_ip":"10.42.0.200"}}]`},
		{"lease range", "subnets", `[{"subnet":"10.42.0.0/24","gateway":"10.42.0.1","lease_range":{"start_ip":"10.42.0.20","end_ip":"10.42.0.200"}}]`},
		{"route destination", "routes", `[{"destination":"198.51.100.0/24","gateway":"10.42.0.2","metric":10}]`},
		{"route gateway", "routes", `[{"destination":"192.0.2.0/24","gateway":"10.42.0.3","metric":10}]`},
		{"route metric", "routes", `[{"destination":"192.0.2.0/24","gateway":"10.42.0.2","metric":20}]`},
		{"ipv6", "ipv6_enabled", `false`},
		{"isolation", "internal", `true`},
		{"dns enablement", "dns_enabled", `false`},
		{"dns servers", "network_dns_servers", `["192.0.2.55","192.0.2.54"]`},
		{"dns priority", "network_dns_servers", `["192.0.2.54","192.0.2.53"]`},
		{"ownership", "labels", `{"dill.stack":"foreign","custom":"retained"}`},
		{"custom labels", "labels", `{"dill.stack":"demo","custom":"changed"}`},
		{"options", "options", `{"mtu":"1400"}`},
		{"ipam", "ipam_options", `{"driver":"dhcp"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := report[tc.field]
			defer func() { report[tc.field] = original }()
			report[tc.field] = json.RawMessage(tc.value)
			if bytes.Equal(baseline, snapshot()) {
				t.Fatal("meaningful network identity/configuration change did not invalidate private evidence")
			}
		})
	}
}
