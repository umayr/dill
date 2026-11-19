package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEnvFile(t *testing.T) {
	const key = "DILL_TEST_VALIDATE_ENV_FILE_IMAGE"
	const body = `output {
  renderer = new JsonRenderer {}
  value = new Dynamic {
    config = new Dynamic {
      services = new Mapping { ["app"] = new Dynamic { image = read("env:DILL_TEST_VALIDATE_ENV_FILE_IMAGE") } }
    }
  }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.pkl")
	envFile := filepath.Join(dir, "validation.env")
	for path, content := range map[string]string{path: body, envFile: key + "=busybox\n"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		prefix bool
		stdin  bool
	}{
		{"global file", true, false},
		{"command file", false, false},
		{"command stdin", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
			args := []string{"-f", path}
			if tc.prefix {
				args = append(args, "--env-file", envFile, "validate")
			} else {
				args = append(args, "validate", "--env-file", envFile)
			}
			if tc.stdin {
				args = append(args, "--stdin")
			}
			args = append(args, "--format", "json")
			out, _, err := captureCommand(t, body, func() error { return run(args) })
			var result map[string]any
			if err != nil || json.Unmarshal([]byte(out), &result) != nil || result["valid"] != true {
				t.Fatalf("err=%v output=%q", err, out)
			}
		})
	}
}

func TestValidateMissingEnvFileJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.env")
	for _, args := range [][]string{{"--env-file", path, "validate"}, {"validate", "--env-file", path}} {
		args = append(args, "--format", "json")
		out, _, err := captureCommand(t, "", func() error { return run(args) })
		var result struct {
			Valid *bool  `json:"valid"`
			Kind  string `json:"kind"`
			Error string `json:"error"`
		}
		if exitCodeFor(err) != 1 || json.Unmarshal([]byte(out), &result) != nil || result.Valid == nil || *result.Valid || result.Kind != "operational_failure" || !strings.Contains(result.Error, "loading env file") {
			t.Errorf("args=%v err=%v output=%q", args, err, out)
		}
	}
}
