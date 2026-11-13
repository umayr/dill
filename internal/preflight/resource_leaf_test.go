package preflight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/orchestrator"
)

func networkEvidence() orchestrator.PreviewResource {
	return orchestrator.PreviewResource{Kind: "network", Name: "synthetic", ActualName: "synthetic", Exists: true, ID: strings.Repeat("a", 64), Settings: json.RawMessage(`{"Driver":"bridge","Scope":"local","Internal":false,"IPv6":false,"Attachable":false,"Ingress":false,"IPAM":{"Driver":"default","Options":null,"Config":[{"Subnet":"10.42.0.0/24","Gateway":"10.42.0.1"},{"Subnet":"10.43.0.0/24","Gateway":"10.43.0.1"}]},"Options":null,"Labels":{"dill.managed":"true","dill.stack":"synthetic"}}`)}
}
func volumeEvidence() orchestrator.PreviewResource {
	return orchestrator.PreviewResource{Kind: "volume", Name: "synthetic_data", ActualName: "synthetic_data", Exists: true, Settings: json.RawMessage(`{"Name":"synthetic_data","Driver":"local","Scope":"local","CreatedAt":"2024-01-01T00:00:00Z","Mountpoint":"/synthetic-private","Options":null,"Labels":{"dill.managed":"true","dill.stack":"synthetic"}}`)}
}
func editSettings(t *testing.T, fact *orchestrator.PreviewResource, mutate func(map[string]any)) {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(fact.Settings, &v); err != nil {
		t.Fatal(err)
	}
	mutate(v)
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	fact.Settings = raw
}
func TestNetworkLeafIdentityRequiredMetadataAndStableConfiguration(t *testing.T) {
	fact := networkEvidence()
	base, err := NetworkDigest("synthetic", fact)
	if err != nil || !ValidDigest(base) {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*orchestrator.PreviewResource){
		"missing actual name":    func(v *orchestrator.PreviewResource) { v.ActualName = "" },
		"mismatched actual name": func(v *orchestrator.PreviewResource) { v.ActualName = "foreign" },
		"short ID":               func(v *orchestrator.PreviewResource) { v.ID = "short" },
		"absent":                 func(v *orchestrator.PreviewResource) { v.Exists = false },
		"missing scope": func(v *orchestrator.PreviewResource) {
			editSettings(t, v, func(m map[string]any) { delete(m, "Scope") })
		},
		"unknown boolean": func(v *orchestrator.PreviewResource) {
			editSettings(t, v, func(m map[string]any) { delete(m, "Internal") })
		},
		"null boolean": func(v *orchestrator.PreviewResource) { editSettings(t, v, func(m map[string]any) { m["IPv6"] = nil }) },
		"missing options": func(v *orchestrator.PreviewResource) {
			editSettings(t, v, func(m map[string]any) { delete(m, "Options") })
		},
		"foreign labels": func(v *orchestrator.PreviewResource) {
			editSettings(t, v, func(m map[string]any) {
				m["Labels"] = map[string]string{"dill.managed": "true", "dill.stack": "foreign"}
			})
		},
		"unknown settings": func(v *orchestrator.PreviewResource) {
			editSettings(t, v, func(m map[string]any) { m["Unknown"] = true })
		},
		"duplicate known field": func(v *orchestrator.PreviewResource) {
			v.Settings = json.RawMessage(strings.Replace(string(v.Settings), `"Driver":"bridge"`, `"Driver":"bridge","Driver":"bridge"`, 1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := networkEvidence()
			mutate(&v)
			if _, err := NetworkDigest("synthetic", v); err == nil {
				t.Fatal("unknown/malformed evidence inferred safe")
			}
		})
	}
	changed := networkEvidence()
	editSettings(t, &changed, func(m map[string]any) {
		ipam := m["IPAM"].(map[string]any)
		subnets := ipam["Config"].([]any)
		subnets[0], subnets[1] = subnets[1], subnets[0]
	})
	if got, err := NetworkDigest("synthetic", changed); err != nil || got != base {
		t.Fatal("subnet order changed commitment", err)
	}
	editSettings(t, &changed, func(m map[string]any) { m["Internal"] = true })
	if got, err := NetworkDigest("synthetic", changed); err != nil || got == base {
		t.Fatal("stable network configuration not bound", err)
	}
}
func TestVolumeLeafNameConfigurationNotIncarnationOrContent(t *testing.T) {
	fact := volumeEvidence()
	base, err := VolumeDigest("synthetic", fact)
	if err != nil {
		t.Fatal(err)
	}
	editSettings(t, &fact, func(m map[string]any) {
		m["CreatedAt"] = "2099-01-01T00:00:00Z"
		m["Mountpoint"] = "/different-synthetic-private"
	})
	if got, err := VolumeDigest("synthetic", fact); err != nil || got != base {
		t.Fatal("timestamp/mountpoint treated as incarnation/content proof", err)
	}
	editSettings(t, &fact, func(m map[string]any) { m["Labels"].(map[string]any)["synthetic"] = "changed" })
	if got, err := VolumeDigest("synthetic", fact); err != nil || got == base {
		t.Fatal("stable configuration not bound", err)
	}
	for _, key := range []string{"Name", "Driver", "Scope", "Options", "Labels"} {
		v := volumeEvidence()
		editSettings(t, &v, func(m map[string]any) { delete(m, key) })
		if _, err := VolumeDigest("synthetic", v); err == nil {
			t.Fatal("missing evidence inferred empty", key)
		}
	}
	fact = volumeEvidence()
	fact.ActualName = "foreign"
	if _, err := VolumeDigest("synthetic", fact); err == nil {
		t.Fatal("lookup name substituted for actual volume")
	}
}
func TestResourceLeavesConsumeActualDockerSDKProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			t.Errorf("metadata projection attempted mutator %s", r.Method)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("API-Version", "1.44")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/_ping":
			w.Write([]byte("OK"))
		case strings.Contains(r.URL.Path, "/networks/"):
			w.Write([]byte(`{"Id":"` + strings.Repeat("a", 64) + `","Name":"synthetic","Driver":"bridge","Scope":"local","EnableIPv6":false,"IPAM":{"Driver":"default","Options":null,"Config":[{"Subnet":"10.42.0.0/24","Gateway":"10.42.0.1","AuxiliaryAddresses":{"synthetic":"10.42.0.2"}}]},"Labels":{"dill.managed":"true","dill.stack":"synthetic"}}`))
		case strings.Contains(r.URL.Path, "/volumes/"):
			w.Write(volumeEvidence().Settings)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	engine, err := orchestrator.NewDockerEngineWithClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	facts, err := engine.PreviewResources(context.Background(), "synthetic", []string{"synthetic_data"})
	if err != nil || len(facts) != 2 {
		t.Fatal("SDK metadata unavailable", err)
	}
	if _, err = NetworkDigest("synthetic", facts[0]); err != nil {
		t.Fatal("actual Docker SDK projection not understood", err)
	}
	if _, err = VolumeDigest("synthetic", facts[1]); err != nil {
		t.Fatal("actual volume SDK projection not understood", err)
	}
}
