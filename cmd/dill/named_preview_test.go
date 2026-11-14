package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func namedPreviewArgs(f *coordinatedDockerFixture, action string) []string {
	return []string{action, "--contract", preflight.Version, "-f", "synthetic.pkl", "-p", "app", "--engine", "docker", "--engine-socket", f.socket, "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", syntheticDaemon, "--coordination-state-dir", f.stateDir, "--coordination-source", "cli"}
}
func TestNamedPreviewSDKFiveRoutes(t *testing.T) {
	for _, action := range []string{"up", "start", "restart", "stop", "down"} {
		t.Run(action, func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			cfg := syntheticConfig()
			useSyntheticConfig(t, cfg)
			var b bytes.Buffer
			err := runNamedPreview(context.Background(), namedPreviewArgs(f, action), &b)
			if err != nil {
				t.Fatalf("preview failed: %v %s", err, b.String())
			}
			var result preflight.Result
			if err = json.Unmarshal(b.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if err = result.Validate(0); err != nil {
				t.Fatal(err)
			}
			if result.Operation != action || len(result.Resources) != 2 {
				t.Fatal("bad result")
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("preview wrote to engine", writes)
			}
		})
	}
}
func TestNamedPreviewInvalidBeforeAnyState(t *testing.T) {
	var b bytes.Buffer
	err := runNamedPreview(context.Background(), []string{"up", "--contract", "wrong"}, &b)
	var pe protocolExit
	if !errors.As(err, &pe) || pe.code != 2 {
		t.Fatalf("%v", err)
	}
	var result preflight.Result
	if err = json.Unmarshal(b.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if err = result.Validate(2); err != nil {
		t.Fatal(err)
	}
	if result.Target != nil || result.CoordinationEffects != "none" {
		t.Fatal("invalid operation accessed target or state")
	}
}
