package core_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/core"
)

// Run in a fresh process because executable discovery is process-cached.
func TestScopedPinnedPkl(t *testing.T) {
	pkl := os.Getenv("DILL_TEST_PINNED_PKL")
	if pkl == "" {
		t.Skip("set DILL_TEST_PINNED_PKL to the verified pinned executable")
	}
	if os.Getenv("DILL_SCOPED_TEST_CHILD") == "1" {
		schema, err := filepath.Abs("../dill.pkl")
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "compose.pkl")
		source := "amends \"" + filepath.ToSlash(schema) + "\"\nconfig { name = read(\"env:BRINE_VAR_NAME\"); services { [\"web\"] { image = \"nginx\" } } }\n"
		if err := os.WriteFile(file, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := core.LoadConfigWithVars(context.Background(), file, map[string]string{"BRINE_VAR_NAME": "scoped-name"})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Name != "scoped-name" {
			t.Fatalf("name = %q", cfg.Name)
		}
		if _, err := core.LoadConfigWithVars(context.Background(), file, nil); err == nil {
			t.Fatal("missing variable accepted")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestScopedPinnedPkl$")
	cmd.Env = append(os.Environ(), "DILL_SCOPED_TEST_CHILD=1", "DILL_PKL_PATH="+pkl, "BRINE_VAR_NAME=ambient")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pinned Pkl subprocess: %v: %s", err, strings.TrimSpace(string(out)))
	}
}
