package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestLiveHostConfigEvidenceParity(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, tc := range []struct {
			name string
			host any
			want LiveConfig
		}{
			{"safe", map[string]any{}, LiveConfig{}},
			{"capabilities", map[string]any{"CapAdd": []string{"SYS_ADMIN"}}, LiveConfig{CapAdd: []string{"SYS_ADMIN"}}},
			{"devices", map[string]any{"Devices": []map[string]string{{"PathOnHost": "/dev/example", "PathInContainer": "/dev/example", "CgroupPermissions": "rwm"}}}, LiveConfig{HasDevices: true}},
			{"tmpfs", map[string]any{"Tmpfs": map[string]string{"/scratch": ""}}, LiveConfig{HasTmpfs: true}},
			{"privileged", map[string]any{"Privileged": true}, LiveConfig{Privileged: true}},
			{"user namespace", map[string]any{"UsernsMode": "host"}, LiveConfig{UserNSMode: "host"}},
			{"pid namespace", map[string]any{"PidMode": "host"}, LiveConfig{PIDMode: "host"}},
			{"ipc namespace", map[string]any{"IpcMode": "host"}, LiveConfig{IPCMode: "host"}},
			{"uts namespace", map[string]any{"UTSMode": "host"}, LiveConfig{UTSMode: "host"}},
			{"cgroup namespace", map[string]any{"CgroupnsMode": "host", "CgroupMode": "host"}, LiveConfig{CgroupNSMode: "host"}},
			{"ordinary private namespaces", map[string]any{"IpcMode": "private", "CgroupnsMode": "private", "CgroupMode": "private"}, LiveConfig{IPCMode: "private", CgroupNSMode: "private"}},
			{"unknown host config", nil, LiveConfig{}},
		} {
			t.Run(adapter+"/"+tc.name, func(t *testing.T) {
				body, _ := json.Marshal(map[string]any{"Id": "immutable", "Config": map[string]any{}, "HostConfig": tc.host, "NetworkSettings": map[string]any{}})
				engine := guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, 200, string(body)), nil }))
				live, err := engine.InspectConfig(context.Background(), "immutable")
				if tc.host == nil {
					if err == nil && live != nil && live.HostConfigKnown {
						t.Fatal("missing actual host config inferred safe")
					}
					return
				}
				if err != nil || live == nil {
					t.Fatal(err)
				}
				actual := LiveConfig{HostConfigKnown: live.HostConfigKnown, HasDevices: live.HasDevices, HasTmpfs: live.HasTmpfs, Privileged: live.Privileged, CapAdd: live.CapAdd, UserNSMode: live.UserNSMode, PIDMode: live.PIDMode, IPCMode: live.IPCMode, UTSMode: live.UTSMode, CgroupNSMode: live.CgroupNSMode}
				want := tc.want
				want.HostConfigKnown = true
				if !reflect.DeepEqual(actual, want) {
					t.Fatalf("actual host evidence lost: got=%+v want=%+v", actual, want)
				}
			})
		}
	}
}

func TestDockerDeviceGrantEvidence(t *testing.T) {
	for _, host := range []string{`{"DeviceRequests":[{"Driver":"example","Count":1}]}`, `{"DeviceCgroupRules":["c 1:3 rwm"]}`} {
		t.Run(host, func(t *testing.T) {
			engine := guardrailAdapterRaw(t, "docker", contextTransport(func(r *http.Request) (*http.Response, error) {
				return contextResponse(r, 200, `{"Id":"immutable","Config":{},"HostConfig":`+host+`,"NetworkSettings":{}}`), nil
			}))
			live, err := engine.InspectConfig(context.Background(), "immutable")
			if err != nil || live == nil || !live.HostConfigKnown || !live.HasDevices {
				t.Fatalf("actual device grant ignored: %+v %v", live, err)
			}
		})
	}
}
