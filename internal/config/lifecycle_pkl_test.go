package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleActualPkl(t *testing.T) {
	if _, err := exec.LookPath("pkl"); err != nil {
		t.Skip("pkl unavailable")
	}
	schema, err := filepath.Abs("../../dill.pkl")
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`amends "file://%s"
config {
  jobs { ["prepare"] { image = "alpine"; command = List("true") } }
  services { ["web"] { image = "nginx"; hooks { beforeUp { new { job = "prepare" } } } } }
}
`, schema)
	path := filepath.Join(t.TempDir(), "compose.pkl")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["web"].Hooks.BeforeUp; len(got) != 1 || got[0].Job != "prepare" || got[0].OnChange != "createOrReplace" {
		t.Fatalf("hooks: %#v", got)
	}
}

func TestLifecycleCombinedCycle(t *testing.T) {
	cfg := &DillConfig{Services: map[string]*Service{"web": {Hooks: &LifecycleHooks{BeforeUp: []Hook{{Job: "one"}}}}, "db": {DependsOn: []string{"web"}}}, Jobs: map[string]*Job{"one": {Image: "alpine", Command: []string{"true"}, AfterJobs: []string{"two"}}, "two": {Image: "alpine", Command: []string{"true"}, DependsOn: []string{"db"}}}}
	if err := validateLifecycle(cfg); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle, got %v", err)
	}
}

func TestHookMissingJob(t *testing.T) {
	var h LifecycleHooks
	if err := decodeStrict([]byte(`{"beforeUp":[{}]}`), &h); err == nil {
		t.Fatal("missing job accepted")
	}
}

func TestOldBasicExampleActualPkl(t *testing.T) {
	if _, err := exec.LookPath("pkl"); err != nil {
		t.Skip("pkl unavailable")
	}
	if _, err := Load(context.Background(), "../../examples/basic/compose.pkl"); err != nil {
		t.Fatal(err)
	}
}
