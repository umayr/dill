package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedOrphansNeverExpandSelectionOrWrite(t *testing.T) {
	for _, noPrune := range []bool{false, true} {
		t.Run(map[bool]string{true: "no-prune", false: "default"}[noPrune], func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			args := namedPreviewArgs(f, "up")
			if noPrune {
				args = append(args, "--no-prune")
			}
			var output bytes.Buffer
			if err := runNamedPreview(context.Background(), args, &output); err != nil {
				t.Fatal(err)
			}
			var review preflight.Result
			if err := json.Unmarshal(output.Bytes(), &review); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.orphan = true
			f.mu.Unlock()
			apply := append([]string{}, args...)
			apply[2] = "dill.operation/1"
			apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
			result, code, _ := captureOperation(t, "", apply)
			if code == 0 || result.Effects != "none" {
				t.Fatalf("orphan accepted: %+v", result)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("orphan caused engine writes")
			}
		})
	}
}
