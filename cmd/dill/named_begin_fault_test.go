package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedPartialBeginRetainsCorrelatedIDAndUnknownMetadata(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	old := beginCoordinatedOperation
	defer func() { beginCoordinatedOperation = old }()
	id := "op_" + strings.Repeat("a", 32)
	beginCoordinatedOperation = func(context.Context, *coordination.State, *coordination.Target, string, string, []coordination.ResourceInput) (*coordination.Session, error) {
		return nil, &coordination.StartError{OperationID: id}
	}
	var output bytes.Buffer
	err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output)
	var result preflight.Result
	if e := json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if err == nil || result.OperationID != id || result.Outcome != "unknown" || result.CoordinationEffects != "unknown" || result.EngineEffects != "none" || result.Target == nil || len(result.Resources) != 0 {
		t.Fatalf("bad partial begin: %+v", result)
	}
	if e := result.Validate(1); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("partial begin mutated engine")
	}
}
