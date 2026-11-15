package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedChangedContainerIDRejectsEveryRouteBeforeWrites(t *testing.T) {
	for _, action := range []string{"up", "start", "restart", "stop", "down"} {
		t.Run(action, func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			args := namedPreviewArgs(f, action)
			var output bytes.Buffer
			if err := runNamedPreview(context.Background(), args, &output); err != nil {
				t.Fatal(err)
			}
			var review preflight.Result
			if err := json.Unmarshal(output.Bytes(), &review); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.liveContainerID = strings.Repeat("f", 64)
			f.mu.Unlock()
			apply := append([]string{}, args...)
			apply[2] = "dill.operation/1"
			apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
			result, code, _ := captureOperation(t, "", apply)
			if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_MISMATCH" {
				t.Fatalf("bad mismatch %+v", result)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatalf("%s mutated before comparison", action)
			}
		})
	}
}
