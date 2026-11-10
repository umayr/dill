package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/umayr/dill/internal/config"
)

// These are wire-contract tests, not evidence of kernel enforcement.
func TestResourceCreateRequests(t *testing.T) {
	shares, pids := 512, 100
	svc := &config.Service{Image: "busybox", MemLimit: "128m", MemReservation: "32m", CPUs: "0.123456789", CPUShares: &shares, PidsLimit: &pids, Cpuset: "0-1"}
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			var body map[string]any
			transport := contextTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/containers/create") {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					return contextResponse(r, http.StatusCreated, `{"Id":"created-container-id"}`), nil
				}
				return contextResponse(r, http.StatusNoContent, ""), nil
			})
			var e Engine
			if engine == "docker" {
				cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				e = &DockerEngine{cli: cli}
			} else {
				p, conn := newContextTestEngine(t, context.Background())
				conn.Client.Transport = transport
				e = p
			}
			if _, err := e.StartService(context.Background(), "app", svc, "stack"); err != nil {
				t.Fatal(err)
			}
			if body == nil {
				t.Fatal("missing create request")
			}
			var expected map[string]any
			if engine == "docker" {
				expected = map[string]any{"Memory": float64(128 << 20), "MemoryReservation": float64(32 << 20), "NanoCpus": float64(123450000), "CpuShares": float64(512), "PidsLimit": float64(100), "CpusetCpus": "0-1"}
				body = body["HostConfig"].(map[string]any)
			} else {
				res := body["resource_limits"].(map[string]any)
				mem := res["memory"].(map[string]any)
				cpu := res["cpu"].(map[string]any)
				pid := res["pids"].(map[string]any)
				expected = map[string]any{"limit": float64(128 << 20), "reservation": float64(32 << 20)}
				for key, want := range expected {
					if mem[key] != want {
						t.Errorf("memory %s = %v, want %v", key, mem[key], want)
					}
				}
				expected = map[string]any{"shares": float64(512), "period": float64(100000), "quota": float64(12345), "cpus": "0-1"}
				for key, want := range expected {
					if cpu[key] != want {
						t.Errorf("cpu %s = %v, want %v", key, cpu[key], want)
					}
				}
				if pid["limit"] != float64(100) {
					t.Errorf("pids = %v", pid)
				}
				return
			}
			for key, want := range expected {
				if body[key] != want {
					t.Errorf("%s = %v, want %v", key, body[key], want)
				}
			}
		})
	}
}

func TestResourceInvalidBeforeAdapterRequest(t *testing.T) {
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			called := false
			transport := contextTransport(func(r *http.Request) (*http.Response, error) { called = true; return contextResponse(r, 500, ""), nil })
			var e Engine
			if name == "docker" {
				cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				e = &DockerEngine{cli: cli}
			} else {
				p, conn := newContextTestEngine(t, context.Background())
				conn.Client.Transport = transport
				e = p
			}
			_, err := e.StartService(context.Background(), "app", &config.Service{Image: "busybox", MemLimit: "1pb"}, "stack")
			if err == nil || !strings.Contains(err.Error(), "memLimit") || called {
				t.Fatalf("err=%v, request=%v", err, called)
			}
		})
	}
}

func TestResourceInspectResponses(t *testing.T) {
	for _, name := range []string{"docker", "podman"} {
		t.Run(name, func(t *testing.T) {
			payload := `{"Id":"container-id", "ImageName":"busybox", "Config":{"Image":"busybox"}, "NetworkSettings":{}, "HostConfig":{"Memory":134217728,"MemoryReservation":33554432,"NanoCpus":123450000,"CpuShares":512,"PidsLimit":100,"CpusetCpus":"0-1"}}`
			transport := contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, 200, payload), nil })
			var e Engine
			if name == "docker" {
				cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.44"), client.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				e = &DockerEngine{cli: cli}
			} else {
				p, conn := newContextTestEngine(t, context.Background())
				conn.Client.Transport = transport
				e = p
			}
			got, err := e.InspectConfig(context.Background(), "app")
			if err != nil {
				t.Fatal(err)
			}
			want := config.ResourceLimits{Memory: 128 << 20, MemoryReservation: 32 << 20, NanoCPUs: 123450000, CPUShares: 512, PidsLimit: 100}
			if got.Resources != want || got.Cpuset != "0-1" {
				t.Fatalf("resources = %+v cpuset = %q", got.Resources, got.Cpuset)
			}
		})
	}
}
