package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedPreviewFinalizationFailuresWithdrawReview(t *testing.T) {
	for _, stage := range []string{"close", "commit", "release"} {
		t.Run(stage, func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			oldClose, oldFinish, oldRelease := closeCoordinatedTarget, finishNamedSession, closeCoordinatedSession
			defer func() {
				closeCoordinatedTarget, finishNamedSession, closeCoordinatedSession = oldClose, oldFinish, oldRelease
			}()
			switch stage {
			case "close":
				closeCoordinatedTarget = func(target *coordination.Target) error { _ = oldClose(target); return errors.New("synthetic close") }
			case "commit":
				finishNamedSession = func(*coordination.Session, string, string) error { return errors.New("synthetic commit") }
			case "release":
				closeCoordinatedSession = func(session *coordination.Session) error {
					_ = oldRelease(session)
					return errors.New("synthetic release")
				}
			}
			var output bytes.Buffer
			err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &output)
			var result preflight.Result
			if e := json.Unmarshal(output.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			if err == nil || result.Outcome != "unknown" || result.EngineEffects != "none" || result.CoordinationEffects != "unknown" || len(result.Resources) != 0 || result.InputDigest != nil || result.SelectionDigest != nil {
				t.Fatalf("bad failure: %+v err=%v", result, err)
			}
			if e := result.Validate(1); e != nil {
				t.Fatal(e)
			}
			want := map[string]string{"close": "ENGINE_CLOSE_FAILED", "commit": "COORDINATION_COMMIT_FAILED", "release": "COORDINATION_RELEASE_FAILED"}[stage]
			if len(result.Errors) != 1 || result.Errors[0].Code != want {
				t.Fatalf("wrong error %+v", result.Errors)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatalf("preview wrote %d times", writes)
			}
		})
	}
}
