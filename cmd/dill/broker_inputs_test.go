package main

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBrokerInputsReachOnlyMachineEvaluation(t *testing.T) {
	// FindPkl intentionally pins its first executable for the process lifetime.
	// Keep the synthetic evaluator out of the rest of the CLI test process.
	if os.Getenv("DILL_TEST_BROKER_INPUTS_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBrokerInputsReachOnlyMachineEvaluation$")
		cmd.Env = append(os.Environ(), "DILL_TEST_BROKER_INPUTS_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated evaluator test: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	pkl := filepath.Join(dir, "pkl")
	script := `#!/bin/sh
printf '{"config":{"name":"%s","engine":"docker","services":{}}}' "${BRINE_VAR_DOMAIN-unset}"
`
	if err := os.WriteFile(pkl, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DILL_PKL_PATH", pkl)
	t.Setenv("BRINE_VAR_DOMAIN", "ambient-not-approved")
	source := filepath.Join(dir, "compose.pkl")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	load := func() (*config.DillConfig, error) {
		ctx, err := brokerInputContext(context.Background(), source)
		if err != nil {
			return nil, err
		}
		return config.Load(ctx, source)
	}
	cfg, err := load()
	if err != nil || cfg.Name != "unset" {
		t.Fatal("ambient configuration leaked into broker evaluation", err)
	}
	raw, err := brokersecret.EncodeInputs(map[string]string{"BRINE_VAR_DOMAIN": "captured"})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, brokersecret.InputsFile)
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = load()
	if err != nil || cfg.Name != "captured" {
		t.Fatal("captured inputs did not reach machine evaluator", err)
	}
	if err = os.WriteFile(file, []byte(`{"contract":"dill.broker-inputs/1","variables":{"PATH":"attacker"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = load(); err == nil {
		t.Fatal("unsafe source env accepted")
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(source, file); err != nil {
		t.Fatal(err)
	}
	if _, err = load(); err == nil {
		t.Fatal("symlink inputs accepted")
	}
}
