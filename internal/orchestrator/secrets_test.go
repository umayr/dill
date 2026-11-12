package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/secretfile"
)

func secretWireEngine(t *testing.T, name string, transport contextTransport) Engine {
	t.Helper()
	if name == "docker" {
		cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cli.Close() })
		return &DockerEngine{cli: cli}
	}
	p, conn := newContextTestEngine(t, context.Background())
	conn.Client.Transport = transport
	return p
}
func TestSecretAdapterRORequests(t *testing.T) {
	// Wire construction only: private builders receive synthetic resolved
	// metadata. Public checked entrypoints are tested separately and never accept
	// this as runtime evidence or contact an unverified local engine.
	svc := &config.Service{Image: "image", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256}}}
	refs := []config.ResolvedSecret{{Name: "db", Source: "db-prod", Revision: "r1", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256, HostPath: "/synthetic-private/source", HostUID: 1000, HostGID: 1000, HostMode: 256}}
	wantHash, _ := config.SecretServiceConfigHash("demo", "app", svc, "lab", refs)
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			e := secretWireEngine(t, name, contextTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/containers/create") {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					return contextResponse(r, 201, `{"Id":"synthetic-created-id"}`), nil
				}
				return contextResponse(r, 204, ""), nil
			}))
			var err error
			if d, ok := e.(*DockerEngine); ok {
				_, err = d.startService(context.Background(), "app", svc, "demo", refs, "lab")
			} else {
				_, err = e.(*PodmanEngine).startService(context.Background(), "app", svc, "demo", refs, "lab")
			}
			if err != nil {
				t.Fatal(err)
			}
			if body == nil {
				t.Fatal("create request missing")
			}
			var labels map[string]any
			if name == "docker" {
				labels = body["Labels"].(map[string]any)
				host := body["HostConfig"].(map[string]any)
				if v := host["UsernsMode"]; v != nil && v != "" {
					t.Fatal("container namespace overridden")
				}
				mounts := host["Mounts"].([]any)
				if len(mounts) != 1 {
					t.Fatal("wrong mount count")
				}
				m := mounts[0].(map[string]any)
				if m["Type"] != "bind" || m["Source"] != refs[0].HostPath || m["Target"] != refs[0].Target || m["ReadOnly"] != true {
					t.Fatal("not an exact read-only bind")
				}
				if b, ok := m["BindOptions"].(map[string]any); ok && b["CreateMountpoint"] == true {
					t.Fatal("source auto-creation enabled")
				}
			} else {
				labels = body["labels"].(map[string]any)
				mounts := body["mounts"].([]any)
				if len(mounts) != 1 {
					t.Fatal("wrong mount count")
				}
				m := mounts[0].(map[string]any)
				if m["type"] != "bind" || m["source"] != refs[0].HostPath || m["destination"] != refs[0].Target {
					t.Fatal("not an exact host bind")
				}
				options := m["options"].([]any)
				if len(options) != 1 || options[0] != "ro" {
					t.Fatal("unexpected relabel/idmap/write mount options")
				}
				if ns, ok := body["userns"].(map[string]any); ok {
					if mode := ns["nsmode"]; mode != nil && mode != "" && mode != "default" {
						t.Fatal("normal user namespace overridden")
					}
				}
				if body["idmappings"] != nil {
					t.Fatal("per-container ID mapping overridden")
				}
			}
			if labels[SecretEnabledLabel] != "true" || labels["dill.config-hash"] != wantHash {
				t.Fatal("missing marker/reference hash")
			}
			encoded, _ := json.Marshal(labels)
			if strings.Contains(string(encoded), "synthetic-private") {
				t.Fatal("source path included in labels")
			}
		})
	}
}
func TestSecretAdapterUncheckedAndMismatchedRequestsFailBeforeSDK(t *testing.T) {
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			e := secretWireEngine(t, name, contextTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return contextResponse(r, 500, `{"message":"synthetic-private-error"}`), nil
			}))
			svc := &config.Service{Image: "image", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", Mode: 256}}}
			if _, err := e.StartService(context.Background(), "app", svc, "demo"); err != config.ErrSecretUnavailable {
				t.Fatal("unchecked delivery allowed")
			}
			if _, err := e.(CheckedSecretCreator).StartServiceChecked(context.Background(), "app", svc, "demo", secretfile.Request{Engine: name, Socket: "/synthetic.sock", Stack: "demo"}); err != config.ErrSecretUnavailable {
				t.Fatal("mismatched endpoint accepted")
			}
			svc.Secrets = nil
			svc.Labels = map[string]string{SecretEnabledLabel: "true"}
			if _, err := e.StartService(context.Background(), "app", svc, "demo"); err != config.ErrSecretUnavailable {
				t.Fatal("reserved marker override allowed")
			}
			if calls != 0 {
				t.Fatal("denial made SDK request")
			}
		})
	}
}
func TestSecretInspectMetadataParity(t *testing.T) {
	body := `{"Id":"synthetic-id","Config":{"Image":"image","Labels":{}},"HostConfig":{},"NetworkSettings":{"Ports":{}},"Mounts":[{"Type":"bind","Source":"/synthetic-private/source","Destination":"/run/secrets/db","RW":false}]}`
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			e := secretWireEngine(t, name, contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, 200, body), nil }))
			live, err := e.InspectConfig(context.Background(), "id")
			if err != nil {
				t.Fatal(err)
			}
			if !live.MountsKnown || len(live.Volumes) != 1 || live.Volumes[0].Type != "bind" || live.Volumes[0].Source != "/synthetic-private/source" || live.Volumes[0].Target != "/run/secrets/db" || !live.Volumes[0].ReadOnly {
				t.Fatalf("inspect lost secret mount metadata: %+v", live)
			}
		})
	}
}
