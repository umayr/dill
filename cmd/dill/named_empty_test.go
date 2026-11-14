package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedEmptyGraphStartRestartEnsureNetwork(t *testing.T) {
	for _, action := range []string{"start", "restart"} {
		t.Run(action, func(t *testing.T) {
			f := newCoordinatedDocker(t, false)
			cfg := &config.DillConfig{Name: "app", Engine: "docker", Services: map[string]*config.Service{}}
			useSyntheticConfig(t, cfg)
			args := namedPreviewArgs(f, action)
			var out bytes.Buffer
			if err := runNamedPreview(context.Background(), args, &out); err != nil {
				t.Fatalf("%v: %s", err, out.String())
			}
			var preview preflight.Result
			if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			if len(preview.Resources) != 1 || preview.Resources[0].Kind != "network" || preview.Resources[0].Presence != "absent" || preview.Resources[0].Action != "ensure" {
				t.Fatalf("bad empty graph: %+v", preview.Resources)
			}
			apply := append([]string{}, args...)
			apply[2] = "dill.operation/1"
			apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *preview.SelectionDigest)
			result, code, _ := captureOperation(t, "", apply)
			if code != 0 || result.Outcome != "succeeded" {
				t.Fatalf("apply %+v exit %d", result, code)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 1 {
				t.Fatalf("expected network ensure, got %d writes", writes)
			}
		})
	}
}
