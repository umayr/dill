package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedStateOpenFailureIsUnknownWithoutEngineEffects(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	args := namedPreviewArgs(f, "up")
	replaceNamedFlag(args, "--coordination-state-dir", filepath.Join(t.TempDir(), "not-present"))
	var output bytes.Buffer
	err := runNamedPreview(context.Background(), args, &output)
	var result preflight.Result
	if e := json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if err == nil || result.Target == nil || result.Outcome != "unknown" || result.CoordinationEffects != "unknown" || result.EngineEffects != "none" || len(result.Resources) != 0 || result.SelectionDigest != nil || result.Errors[0].Code != "COORDINATION_UNAVAILABLE" {
		t.Fatalf("bad open failure: %+v", result)
	}
	if e := result.Validate(1); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("open failure wrote engine")
	}
}
