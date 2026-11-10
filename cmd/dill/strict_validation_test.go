package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/orchestrator"
)

func TestUntrustedPklOutputRejectedBeforeEngine(t *testing.T) {
	cases := []struct{ name, output, want string }{
		{"config field", `{"config":{"services":{},"unsupported":true}}`, `unknown property "unsupported"`},
		{"service field", `{"config":{"services":{"app":{"image":"x","privileged":true}}}}`, `unknown property "privileged"`},
		{"case variant", `{"config":{"services":{"app":{"image":"x","Image":"y"}}}}`, `unknown property "Image"`},
		{"port field", `{"config":{"services":{"app":{"image":"x","ports":[{"target":80,"unsupported":true}]}}}}`, `unknown property "unsupported"`},
		{"mount field", `{"config":{"services":{"app":{"image":"x","volumes":[{"target":"/data","unsupported":true}]}}}}`, `unknown property "unsupported"`},
		{"duration field", `{"config":{"services":{"app":{"image":"x","healthcheck":{"test":"ok","interval":{"value":1,"unit":"s","unsupported":true}}}}}}`, `unknown property "unsupported"`},
		{"missing services", `{"config":{}}`, `config.services`},
		{"null services", `{"config":{"services":null}}`, `config.services`},
		{"missing image", `{"config":{"services":{"app":{}}}}`, `.image`},
		{"empty image", `{"config":{"services":{"app":{"image":""}}}}`, `.image`},
		{"bad engine", `{"config":{"engine":"invalid","services":{}}}`, `config.engine`},
		{"bad restart", `{"config":{"services":{"app":{"image":"x","restart":"invalid"}}}}`, `.restart`},
		{"bad pull policy", `{"config":{"services":{"app":{"image":"x","pullPolicy":"invalid"}}}}`, `.pullPolicy`},
		{"missing probe", `{"config":{"services":{"app":{"image":"x","healthcheck":{}}}}}`, `.healthcheck.test`},
		{"negative retries", `{"config":{"services":{"app":{"image":"x","healthcheck":{"test":"ok","retries":-1}}}}}`, `healthcheck retries`},
		{"duration overflow", `{"config":{"services":{"app":{"image":"x","healthcheck":{"test":"ok","interval":{"value":1e30,"unit":"s"}}}}}}`, `healthcheck interval`},
		{"duplicate service", `{"config":{"services":{"app":{"image":"x"},"app":{"image":"y"}}}}`, `duplicate key "app"`},
		{"duplicate label", `{"config":{"services":{"app":{"image":"x","labels":{"foo":"x","foo":"y"}}}}}`, `duplicate key "foo"`},
	}
	old := engineFactory
	opened := false
	engineFactory = func(context.Context, string) (orchestrator.Engine, error) {
		opened = true
		return nil, fmt.Errorf("unexpected engine")
	}
	defer func() { engineFactory = old }()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.pkl")
			body := "output { text = #\"\"\"\n" + tc.output + "\n\"\"\"# }\n"
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			for _, format := range []string{"text", "json"} {
				out, _, err := captureCommand(t, body, func() error {
					return run([]string{"-f", path, "validate", "--stdin", "--format", format})
				})
				if err == nil || !strings.Contains(err.Error(), tc.want) || exitCodeFor(err) != 2 {
					t.Fatalf("stdin %s: err=%v output=%s", format, err, out)
				}
				if format == "json" {
					var v map[string]any
					if json.Unmarshal([]byte(out), &v) != nil || v["kind"] != "invalid_input" {
						t.Fatalf("stdin JSON output=%s", out)
					}
				}
			}
			for _, command := range []string{"validate", "plan", "diff", "up"} {
				args := []string{"-f", path, command}
				if command != "up" {
					args = append(args, "--format", "json")
				}
				out, _, err := captureCommand(t, "", func() error { return run(args) })
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("%s: err=%v output=%s", command, err, out)
				}
				if command != "up" {
					var v map[string]any
					if json.Unmarshal([]byte(out), &v) != nil || v["kind"] != "invalid_input" {
						t.Fatalf("%s: output=%s", command, out)
					}
				}
			}
		})
	}
	if opened {
		t.Fatal("engine opened for invalid output")
	}
}

func TestEffectiveContainerNameCollision(t *testing.T) {
	schema, err := filepath.Abs("../../dill.pkl")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, configName, override, container string }{{"configured", "stack", "", "stack_web"}, {"override", "other", "chosen", "chosen_web"}, {"derived", "", "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.pkl")
			container := tc.container
			if tc.name == "derived" {
				container = derivedStackName(path) + "_web"
			}
			body := fmt.Sprintf("amends %q\nconfig { name = %q; services { [\"web\"] { image = \"x\" }; [\"other\"] { image = \"x\"; containerName = %q } } }\n", filepath.ToSlash(schema), tc.configName, container)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			old := engineFactory
			opened := false
			engineFactory = func(context.Context, string) (orchestrator.Engine, error) {
				opened = true
				return nil, fmt.Errorf("unexpected engine")
			}
			defer func() { engineFactory = old }()
			for _, command := range []string{"validate", "plan", "diff", "up"} {
				args := []string{"-f", path}
				if tc.override != "" {
					args = append(args, "-p", tc.override)
				}
				args = append(args, command)
				_, _, err := captureCommand(t, "", func() error { return run(args) })
				if err == nil || !strings.Contains(err.Error(), "conflicts with service") {
					t.Fatalf("%s: %v", command, err)
				}
			}
			args := []string{"-f", path}
			if tc.override != "" {
				args = append(args, "-p", tc.override)
			}
			args = append(args, "validate", "--stdin", "--format", "json")
			out, _, err := captureCommand(t, body, func() error { return run(args) })
			var result map[string]any
			if err == nil || json.Unmarshal([]byte(out), &result) != nil || result["kind"] != "invalid_input" {
				t.Fatalf("stdin: err=%v output=%s", err, out)
			}
			if opened {
				t.Fatal("engine opened")
			}
		})
	}
}

func TestSupportedSchemaOutputAndExtraModuleFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.pkl")
	body := `output { text = #"""
{"extraModuleProperty":true,"config":{"name":"demo","engine":"docker","services":{"app":{"image":"busybox","containerName":"custom","restart":"no","pullPolicy":"missing","environment":{"X":"Y"},"volumes":[{"type":"volume","source":"data","target":"/data","readOnly":false,"externalVolume":false,"name":"named"}],"ports":[{"target":80,"published":8080,"protocol":"tcp","hostIp":"127.0.0.1"}],"labels":{"custom":"label"},"dependsOn":[],"command":["echo"],"networkMode":"host","healthcheck":{"test":"true","interval":{"value":1,"unit":"s"},"timeout":{"value":1,"unit":"s"},"retries":1,"startPeriod":{"value":1,"unit":"s"}},"user":"1000","init":true,"capAdd":["CHOWN"],"devices":[],"cpuset":"0","memLimit":"512m","memReservation":"128m","cpus":"0.5","cpuShares":512,"pidsLimit":100}}}}
"""# }
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureCommand(t, "", func() error { return run([]string{"-f", path, "validate", "--format", "json"}) })
	if err != nil {
		t.Fatal(err)
	}
}

func TestEvaluatorFailureClassification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.pkl")
	if err := os.WriteFile(path, []byte("this is not pkl"), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureCommand(t, "", func() error { return run([]string{"-f", path, "validate", "--format", "json"}) })
	var result map[string]any
	if err == nil || exitCodeFor(err) != 1 || json.Unmarshal([]byte(out), &result) != nil || result["kind"] != "evaluator_failure" {
		t.Fatalf("err=%v output=%s", err, out)
	}
	if err := os.WriteFile(path, []byte(`amends "missing-import.pkl"`), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err = captureCommand(t, "", func() error { return run([]string{"-f", path, "plan", "--format", "json"}) })
	result = nil
	if err == nil || exitCodeFor(err) != 1 || json.Unmarshal([]byte(out), &result) != nil || result["kind"] != "evaluator_failure" {
		t.Fatalf("import err=%v output=%s", err, out)
	}
}
