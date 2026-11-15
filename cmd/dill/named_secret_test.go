package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedBoundLateSecretClearsTargetAndStack(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	args := namedPreviewArgs(f, "up")
	var output bytes.Buffer
	if err := runNamedPreview(context.Background(), args, &output); err != nil {
		t.Fatal(err)
	}
	var review preflight.Result
	if err := json.Unmarshal(output.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.labels["dill.secret-enabled"] = "false"
	f.mu.Unlock()
	apply := append([]string{}, args...)
	apply[2] = "dill.operation/1"
	apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
	result, code, _ := captureOperation(t, "", apply)
	if code != 1 || result.Outcome != "failed" || result.Target != nil || result.Stack != "" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "SECRET_UNAVAILABLE" {
		t.Fatalf("secret disclosed: %+v", result)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("secret refusal wrote engine")
	}
}

func TestNamedLiveSecretMarkerWithdrawsTargetAndStack(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	f.labels["dill.secret-enabled"] = "false"
	useSyntheticConfig(t, syntheticConfig())
	var output bytes.Buffer
	err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output)
	var r preflight.Result
	if e := json.Unmarshal(output.Bytes(), &r); e != nil {
		t.Fatal(e)
	}
	if err == nil || r.Outcome != "failed" || r.Target != nil || r.Stack != "" || r.CoordinationEffects != "written" || len(r.Resources) != 0 || len(r.Errors) != 1 || r.Errors[0].Code != "SECRET_UNAVAILABLE" {
		t.Fatalf("secret disclosure or wrong phase: %+v", r)
	}
	if e := r.Validate(1); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("secret refusal wrote engine")
	}
}
