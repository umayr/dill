package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/umayr/dill/internal/orchestrator"
)

func TestValidateOperationV1(t *testing.T) {
	if _, err := exec.LookPath("pkl"); err != nil && os.Getenv("DILL_PKL_PATH") == "" {
		t.Skip("Pkl unavailable")
	}
	schema, err := filepath.Abs("../../dill.pkl")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, content, outcome, stack string
		exit                          int
	}{
		{"valid", "amends \"" + filepath.ToSlash(schema) + "\"\nconfig { name = \"demo\"; services { [\"app\"] { image = \"example\" } } }\n", "succeeded", "demo", 0},
		{"invalid", "amends \"" + filepath.ToSlash(schema) + "\"\nconfig { services { [\"app\"] { image = \"example\"; dependsOn = List(\"missing\") } } }\n", "invalid_input", "", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "compose.pkl")
			if err := os.WriteFile(p, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			old := engineFactory
			defer func() { engineFactory = old }()
			engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
				t.Fatal("validation opened engine")
				return nil, nil
			}
			out, _, err := captureCommand(t, "", func() error {
				return runValidate(context.Background(), commandOptions{ConfigFile: p}, []string{"--format", "operation-v1"})
			})
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit=%d err=%v output=%s", exitCodeFor(err), err, out)
			}
			var got map[string]string
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"version": "dill.validate/1", "operation": "validate", "outcome": tc.outcome, "engineEffects": "none", "stack": tc.stack}
			if len(got) != len(want) {
				t.Fatalf("unexpected fields: %v", got)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("%s=%q want %q", k, got[k], v)
				}
			}
		})
	}
}
