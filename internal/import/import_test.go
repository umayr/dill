package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertAndEvaluate(t *testing.T) {
	pkl := os.Getenv("PKL")
	if pkl == "" {
		pkl = "pkl"
	}
	pkl, err := exec.LookPath(pkl)
	if err != nil {
		if os.Getenv("PKL") != "" {
			t.Fatal(err)
		}
		t.Skip("pinned pkl not installed; set PKL")
	}
	pin, err := os.ReadFile(filepath.Join("..", "loader", "pkl-toolchain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var toolchain struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pin, &toolchain); err != nil || toolchain.Version == "" {
		t.Fatalf("invalid Pkl toolchain pin: %v", err)
	}
	version, err := exec.Command(pkl, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "Pkl "+toolchain.Version+" ") {
		if os.Getenv("PKL") != "" {
			t.Fatalf("PKL must match pinned version %s: %s (%v)", toolchain.Version, version, err)
		}
		t.Skipf("pkl is not pinned version %s; set PKL", toolchain.Version)
	}
	schema, err := filepath.Abs(filepath.Join("..", "..", "dill.pkl"))
	if err != nil {
		t.Fatal(err)
	}
	input := `services:
  web:
    image: nginx:alpine
    environment:
      Z: "slash\\(not_interpolation)"
      A: "some value"
    ports:
      - "127.0.0.1:8080:80/tcp"
    volumes:
      - type: bind
        source: /tmp/source
        target: /data
        read_only: true
        bind: {create_host_path: false}
  worker:
    image: busybox
`
	out, err := Convert(strings.NewReader(input), schema)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Convert(strings.NewReader(input), schema)
	if err != nil || !bytes.Equal(out, again) {
		t.Fatalf("not deterministic: %v", err)
	}
	file := filepath.Join(t.TempDir(), "compose.pkl")
	if err := os.WriteFile(file, out, 0600); err != nil {
		t.Fatal(err)
	}
	encoded, err := exec.CommandContext(context.Background(), pkl, "eval", file, "-f", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("pkl eval: %v\n%s\n%s", err, encoded, out)
	}
	var result struct {
		Config struct {
			Services map[string]struct {
				Image       string            `json:"image"`
				Environment map[string]string `json:"environment"`
				Ports       []string          `json:"ports"`
				Volumes     []struct {
					Type     string `json:"type"`
					Source   string `json:"source"`
					Target   string `json:"target"`
					ReadOnly bool   `json:"readOnly"`
				} `json:"volumes"`
			} `json:"services"`
		} `json:"config"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	s := result.Config.Services["web"]
	if s.Image != "nginx:alpine" || s.Environment["A"] != "some value" || s.Environment["Z"] != `slash\(not_interpolation)` || len(s.Ports) != 1 || len(s.Volumes) != 1 || s.Volumes[0].Source != "/tmp/source" || !s.Volumes[0].ReadOnly {
		t.Fatalf("wrong config: %+v", s)
	}
}

func TestRejectUnsupportedAndAmbiguous(t *testing.T) {
	for name, input := range map[string]string{
		"unknown root":           "services: {}\nnetworks: {}\n",
		"build":                  "services:\n  a:\n    image: nginx\n    build: .\n",
		"duplicate":              "services:\n  a:\n    image: a\n    image: b\n",
		"alias":                  "services:\n  a: &x\n    image: nginx\n  b: *x\n",
		"interpolation":          "services:\n  a:\n    image: ${IMAGE}\n",
		"env inherited":          "services:\n  a:\n    image: nginx\n    environment:\n      PASSWORD:\n",
		"numeric env":            "services:\n  a:\n    image: nginx\n    environment:\n      NUM: 4\n",
		"named volume":           "services:\n  a:\n    image: nginx\n    volumes: ['db:/data']\n",
		"relative bind":          "services:\n  a:\n    image: nginx\n    volumes:\n      - {type: bind, source: ./local, target: /data, bind: {create_host_path: false}}\n",
		"implicit bind creation": "services:\n  a:\n    image: nginx\n    volumes:\n      - {type: bind, source: /tmp/a, target: /data}\n",
		"bind creation enabled":  "services:\n  a:\n    image: nginx\n    volumes:\n      - {type: bind, source: /tmp/a, target: /data, bind: {create_host_path: true}}\n",
		"dependency":             "services:\n  a:\n    image: nginx\n    depends_on: [b]\n  b:\n    image: redis\n",
		"dependency started":     "services: {a: {image: nginx, depends_on: {b: {condition: service_started}}}, b: {image: redis}}",
		"dependency healthy":     "services: {a: {image: nginx, depends_on: {b: {condition: service_healthy}}}, b: {image: redis}}",
		"dependency optional":    "services: {a: {image: nginx, depends_on: {b: {required: false}}}, b: {image: redis}}",
		"dependency restart":     "services: {a: {image: nginx, depends_on: {b: {restart: true}}}, b: {image: redis}}",
		"named volume long":      "services: {a: {image: nginx, volumes: [{type: volume, source: db, target: /data}]}}\nvolumes: {db: {}}",
		"external volume":        "services: {a: {image: nginx, volumes: ['db:/data']}}\nvolumes: {db: {external: true}}",
		"unknown nested bind":    "services: {a: {image: nginx, volumes: [{type: bind, source: /tmp/a, target: /data, bind: {create_host_path: false, propagation: shared}}]}}",
		"unknown root extension": "services: {a: {image: nginx}}\nx-secret_value: value",
		"unquoted port":          "services:\n  a:\n    image: nginx\n    ports: [80]\n",
		"dynamic port":           "services:\n  a:\n    image: nginx\n    ports: ['80']\n",
		"multiple documents":     "services: {a: {image: nginx}}\n---\nservices: {b: {image: redis}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := Convert(strings.NewReader(input), "dill.pkl")
			if err == nil || out != nil {
				t.Fatalf("accepted: %s", out)
			}
		})
	}
}
func TestDiagnosticsDoNotIncludeInput(t *testing.T) {
	for _, input := range []string{
		"services: {a: {image: nginx, secret_value: x}}",
		"services: {a: {image: nginx, volumes: [{type: bind, source: /secret_value, target: relative, bind: {create_host_path: false}}]}}",
		"services: {a: {image: nginx, environment: {SECRET: secret_value, OTHER: '$bad'}}}",
		"services: {a: {image: nginx, depends_on: {secret_value: {condition: service_healthy}}}}",
		"services: {a: {image: nginx, volumes: [{type: volume, source: secret_value, target: /data}]}}",
	} {
		out, err := Convert(strings.NewReader(input), "dill.pkl")
		if err == nil || out != nil || strings.Contains(err.Error(), "secret_value") {
			t.Fatalf("diagnostic leaked input or returned partial output: %v", err)
		}
	}
}

func TestBoundedInputAndSchema(t *testing.T) {
	if _, err := Convert(strings.NewReader(strings.Repeat("x", MaxInputBytes+1)), "dill.pkl"); err == nil {
		t.Fatal("oversized input accepted")
	}
	if _, err := Convert(strings.NewReader("services: {a: {image: nginx}}"), "bad\nname"); err == nil {
		t.Fatal("invalid schema accepted")
	}
}
