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

// All cases use the synthetic SDK transport; hooks call the real close/finish
// before injecting the specified deterministic cancellation or cleanup fault.
func TestNamedCancellationDuringSuccessfulClose(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldClose, oldFinish := closeCoordinatedTarget, finishNamedSession
	defer func() { closeCoordinatedTarget, finishNamedSession = oldClose, oldFinish }()
	closeCoordinatedTarget = func(target *coordination.Target) error { err := oldClose(target); cancel(); return err }
	journalOutcome, journalEffects := "", ""
	alreadyCancelled := false
	finishNamedSession = func(session *coordination.Session, outcome, effects string) error {
		alreadyCancelled = ctx.Err() != nil
		journalOutcome, journalEffects = outcome, effects
		return oldFinish(session, outcome, effects)
	}
	var output bytes.Buffer
	err := runNamedPreview(ctx, namedPreviewArgs(f, "up"), &output)
	var result preflight.Result
	if e := json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if !alreadyCancelled || journalOutcome != "cancelled" || journalEffects != "none" || exitCodeFor(err) != 1 || result.Outcome != "cancelled" || result.CoordinationEffects != "written" || result.EngineEffects != "none" || result.InputDigest != nil || result.SelectionDigest != nil || len(result.Resources) != 0 || result.Coverage != preflight.EmptyCoverage() || len(result.Errors) != 1 || result.Errors[0].Code != "CANCELLED" || writes != 0 {
		t.Fatalf("bad cancellation: before=%t journal=%s/%s outcome=%s writes=%d err=%v", alreadyCancelled, journalOutcome, journalEffects, result.Outcome, writes, err)
	}
	if e := result.Validate(1); e != nil {
		t.Fatal(e)
	}
}

func TestNamedCancellationAtFinalizerEntry(t *testing.T) {
	f := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	var first bytes.Buffer
	if err := runNamedPreview(context.Background(), namedPreviewArgs(f, "up"), &first); err != nil {
		t.Fatal(err)
	}
	var candidate preflight.Result
	if err := json.Unmarshal(first.Bytes(), &candidate); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err := finalizeNamedPreview(&output, candidate, &operationState{ctx: ctx}, 0)
	var result preflight.Result
	if e := json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if exitCodeFor(err) != 1 || result.Outcome != "cancelled" || result.SelectionDigest != nil || len(result.Resources) != 0 || len(result.Errors) != 1 || result.Errors[0].Code != "CANCELLED" {
		t.Fatalf("entry cancellation lost: %+v %v", result, err)
	}
	if e := result.Validate(1); e != nil {
		t.Fatal(e)
	}
}

func TestNamedCancellationFinalizationBoundaries(t *testing.T) {
	for _, mode := range []string{"after-commit", "close-error", "commit-error", "release-error", "prior-secret"} {
		t.Run(mode, func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			if mode == "prior-secret" {
				f.labels["dill.secret-enabled"] = "false"
			}
			useSyntheticConfig(t, syntheticConfig())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			oldClose, oldFinish, oldRelease := closeCoordinatedTarget, finishNamedSession, closeCoordinatedSession
			defer func() {
				closeCoordinatedTarget, finishNamedSession, closeCoordinatedSession = oldClose, oldFinish, oldRelease
			}()
			journalOutcome, journalEffects := "", ""
			finishNamedSession = func(session *coordination.Session, outcome, effects string) error {
				journalOutcome, journalEffects = outcome, effects
				if mode == "commit-error" {
					return errors.New("synthetic commit failure")
				}
				err := oldFinish(session, outcome, effects)
				if mode == "after-commit" {
					cancel()
				}
				return err
			}
			if mode == "release-error" {
				closeCoordinatedSession = func(session *coordination.Session) error {
					_ = oldRelease(session)
					return errors.New("synthetic release failure")
				}
			}
			if mode != "after-commit" {
				closeCoordinatedTarget = func(target *coordination.Target) error {
					err := oldClose(target)
					cancel()
					if mode == "close-error" {
						return errors.New("synthetic close failure")
					}
					return err
				}
			}
			var output bytes.Buffer
			err := runNamedPreview(ctx, namedPreviewArgs(f, "up"), &output)
			var result preflight.Result
			if e := json.Unmarshal(output.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "after-commit":
				if err != nil || journalOutcome != "succeeded" || journalEffects != "none" || result.Outcome != "succeeded" || result.SelectionDigest == nil {
					t.Fatalf("rewrote durable success: %+v %v", result, err)
				}
			case "close-error":
				if err == nil || journalOutcome != "unknown" || journalEffects != "unknown" || result.Outcome != "unknown" || result.CoordinationEffects != "unknown" || len(result.Errors) != 2 || result.Errors[0].Code != "CANCELLED" || result.Errors[1].Code != "ENGINE_CLOSE_FAILED" {
					t.Fatalf("lost close uncertainty: %+v %v", result, err)
				}
			case "commit-error", "release-error":
				last := "COORDINATION_COMMIT_FAILED"
				if mode == "release-error" {
					last = "COORDINATION_RELEASE_FAILED"
				}
				if err == nil || journalOutcome != "cancelled" || journalEffects != "none" || result.Outcome != "unknown" || result.CoordinationEffects != "unknown" || len(result.Errors) != 2 || result.Errors[0].Code != "CANCELLED" || result.Errors[1].Code != last {
					t.Fatalf("lost finalization uncertainty: %+v %v", result, err)
				}
			case "prior-secret":
				if err == nil || journalOutcome != "failed" || journalEffects != "none" || result.Outcome != "failed" || result.Target != nil || result.Stack != "" || len(result.Errors) != 1 || result.Errors[0].Code != "SECRET_UNAVAILABLE" {
					t.Fatalf("replaced secret primary: %+v %v", result, err)
				}
			}
			exit := 1
			if mode == "after-commit" {
				exit = 0
			}
			if e := result.Validate(exit); e != nil {
				t.Fatal(e)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("preview mutated engine")
			}
		})
	}
}
