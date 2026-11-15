package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestNamedPreviewBoundApplySDKAllRoutes(t *testing.T) {
	for _, action := range []string{"up", "start", "restart", "stop", "down"} {
		t.Run(action, func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			args := namedPreviewArgs(f, action)
			var out bytes.Buffer
			if err := runNamedPreview(context.Background(), args, &out); err != nil {
				t.Fatalf("preview %v %s", err, out.String())
			}
			var preview preflight.Result
			if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			apply := append([]string{}, args...)
			apply[2] = "dill.operation/1"
			apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *preview.SelectionDigest)
			result, code, stderr := captureOperation(t, "", apply)
			if action == "down" {
				if code != 1 || result.Outcome != "failed" || result.Effects != "partial" {
					t.Fatalf("down semantics: %+v code %d", result, code)
				}
			} else if code != 0 || result.Outcome != "succeeded" {
				t.Fatalf("apply %s code %d stderr %s result %+v", action, code, stderr, result)
			}
			if result.OperationID == preview.OperationID {
				t.Fatal("operation IDs reused")
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes == 0 {
				t.Fatal("expected execution did not reach engine")
			}
		})
	}
}
func TestNamedPreparedImageMovementBeforeEnsure(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	useSyntheticConfig(t, syntheticConfig())
	args := namedPreviewArgs(f, "up")
	var out bytes.Buffer
	if err := runNamedPreview(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	var review preflight.Result
	if err := json.Unmarshal(out.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.preparedImageID = "sha256:" + strings.Repeat("f", 64)
	f.mu.Unlock()
	apply := append([]string{}, args...)
	apply[2] = "dill.operation/1"
	apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
	result, code, _ := captureOperation(t, "", apply)
	if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_MISMATCH" {
		t.Fatalf("bad prepared-image mismatch: %+v code=%d", result, code)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatalf("prepared-image mismatch made %d engine writes", writes)
	}
}

func TestNamedMismatchBeforeAllEngineWrites(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	args := namedPreviewArgs(f, "up")
	var out bytes.Buffer
	if err := runNamedPreview(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	var preview preflight.Result
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.image = "synthetic:changed"
	f.mu.Unlock()
	apply := append([]string{}, args...)
	apply[2] = "dill.operation/1"
	apply = append(apply, "--preflight-contract", preflight.Version, "--expect-preflight", *preview.SelectionDigest)
	result, code, _ := captureOperation(t, "", apply)
	if code != 1 || result.Outcome != "failed" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_MISMATCH" {
		t.Fatalf("bad mismatch: %+v code=%d", result, code)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if writes != 0 {
		t.Fatalf("mismatch made %d engine writes", writes)
	}
}
