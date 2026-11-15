package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedPreviewRejectsLiveContainerWithAbsentNetwork(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	f.networkExists = false
	useSyntheticConfig(t, syntheticConfig())
	var output bytes.Buffer
	err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output)
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatalf("contradictory capture caused %d writes", writes)
	}
	var result preflight.Result
	if e := json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if err == nil || result.Outcome != "failed" || result.InputDigest != nil || result.SelectionDigest != nil || len(result.Resources) != 0 || result.Coverage != preflight.EmptyCoverage() {
		t.Fatalf("contradictory evidence exposed a review: err=%v outcome=%s rows=%d", err, result.Outcome, len(result.Resources))
	}
	if e := result.Validate(1); e != nil {
		t.Fatal(e)
	}
}
