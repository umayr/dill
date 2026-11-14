package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedDeclaredResourceLimitBeforeState(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Services: map[string]*config.Service{}}
	for i := 0; i < 256; i++ {
		cfg.Services[fmt.Sprintf("s%d", i)] = &config.Service{Image: "synthetic:v1", PullPolicy: "never"}
	}
	useSyntheticConfig(t, cfg)
	var output bytes.Buffer
	err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output)
	var result preflight.Result
	if e := json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if err == nil || result.Outcome != "failed" || !result.ResourcesTruncated || result.Errors[0].Code != "PREFLIGHT_LIMIT_EXCEEDED" || result.CoordinationEffects != "none" || result.Target != nil {
		t.Fatalf("wrong limit result %+v", result)
	}
	if e := result.Validate(1); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(f.stateDir)
	if e != nil || len(entries) != 0 {
		t.Fatal("limit accessed state", e)
	}
	f.mu.Lock()
	writes := f.writes
	connections := f.connections
	f.mu.Unlock()
	if writes != 0 || connections != 0 {
		t.Fatal("limit contacted engine")
	}
}
