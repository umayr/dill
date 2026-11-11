package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/orchestrator"
)

func TestOfflineValidationEntryPoints(t *testing.T) {
	if _, err := exec.LookPath("pkl"); err != nil && os.Getenv("DILL_PKL_PATH") == "" {
		t.Skip("Pkl unavailable")
	}
	schema, err := filepath.Abs("../../dill.pkl")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, fields, want string }{
		{"missing", `dependsOn = List("missing")`, "dependsOn"},
		{"cycle", `dependsOn = List("app")`, "cycle detected"},
		{"label", `labels { ["dill.owner"] = "x" }`, "reserved"},
		{"port", `ports = List("99999:80")`, "port"},
		{"mount", `volumes = List(":")`, "volume"},
		{"tmpfs source", `volumes = List(new Mount { type = "tmpfs"; source = "/host"; target = "/data" })`, "tmpfs mounts do not support a source"},
		{"resource", `memLimit = "bogus"`, "memLimit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf("amends %q\nconfig { services { [\"app\"] { image = \"example\"; %s } } }\n", filepath.ToSlash(schema), tt.fields)
			path := filepath.Join(t.TempDir(), "compose.pkl")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"file-text", "file-json", "stdin-text", "stdin-json"} {
				t.Run(mode, func(t *testing.T) {
					args := []string{}
					if strings.Contains(mode, "stdin") {
						args = append(args, "--stdin")
					}
					if strings.Contains(mode, "json") {
						args = append(args, "--format", "json")
					}
					out, _, err := captureCommand(t, body, func() error { return runValidate(context.Background(), commandOptions{ConfigFile: path}, args) })
					if err == nil || !strings.Contains(err.Error(), tt.want) {
						t.Fatalf("err=%v output=%s", err, out)
					}
					if exitCodeFor(err) != 2 {
						t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
					}
					if strings.Contains(mode, "json") {
						var v struct {
							Valid bool   `json:"valid"`
							Kind  string `json:"kind"`
						}
						if err := json.Unmarshal([]byte(out), &v); err != nil || v.Valid || v.Kind != "invalid_input" {
							t.Fatalf("JSON=%s err=%v", out, err)
						}
					}
				})
			}
			old := engineFactory
			opened := false
			engineFactory = func(context.Context, string) (orchestrator.Engine, error) {
				opened = true
				return nil, fmt.Errorf("engine should not open")
			}
			defer func() { engineFactory = old }()
			for _, command := range []string{"plan", "diff", "up"} {
				args := []string{"-f", path, command}
				if command != "up" {
					args = append(args, "--format", "json")
				}
				out, _, err := captureCommand(t, "", func() error { return run(args) })
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("%s err=%v output=%s", command, err, out)
				}
				if command != "up" {
					var v map[string]any
					if json.Unmarshal([]byte(out), &v) != nil || v["kind"] != "invalid_input" {
						t.Fatalf("%s output=%s", command, out)
					}
				}
			}
			if opened {
				t.Fatal("engine opened for invalid input")
			}
		})
	}
}
