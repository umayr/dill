package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

type finalizerEngine struct {
	orchestrator.Engine
	closeErr error
	closed   int
}

func (e *finalizerEngine) Close() error { e.closed++; return e.closeErr }

type failingResultWriter struct{}

func (failingResultWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestEmitOperationResultFinalizesBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		closeErr                           error
		unfinished                         bool
		wantOutcome, wantEffects, wantCode string
	}{
		{"success", nil, false, "succeeded", "none", ""},
		{"uncertain failure", nil, true, "unknown", "unknown", "OPERATION_FAILED"},
		{"close failure", errors.New("private engine diagnostic"), false, "unknown", "unknown", "ENGINE_CLOSE_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &finalizerEngine{closeErr: tc.closeErr}
			recorder := &operation.Recorder{}
			state := &operationState{engine: &operation.Engine{Engine: engine}, recorder: recorder}
			result := operation.Result{Outcome: "succeeded", Errors: []operation.Error{}}
			code, errorCode, message := 0, "", ""
			if tc.unfinished {
				recorder.Begin("container", "private-id", "start")
				result.Outcome = "failed"
				code, errorCode, message = 1, "OPERATION_FAILED", "safe message"
			}
			var out bytes.Buffer
			err := emitOperationResult(&out, state, &result, code, errorCode, message)
			if (err != nil) != (tc.wantCode != "") {
				t.Fatalf("unexpected exit: %v", err)
			}
			if engine.closed != 1 || state.engine != nil {
				t.Fatalf("engine closed %d times", engine.closed)
			}
			var got operation.Result
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Outcome != tc.wantOutcome || got.Effects != tc.wantEffects {
				t.Fatalf("outcome/effects: %+v", got)
			}
			if tc.wantCode != "" && (len(got.Errors) != 1 || got.Errors[0].Code != tc.wantCode) {
				t.Fatalf("errors: %+v", got.Errors)
			}
			if bytes.Contains(out.Bytes(), []byte("private")) {
				t.Fatal("private diagnostics leaked")
			}
		})
	}
}

func TestEmitOperationResultWriteFailure(t *testing.T) {
	state := &operationState{recorder: &operation.Recorder{}}
	result := operation.Result{Outcome: "succeeded"}
	if err := emitOperationResult(failingResultWriter{}, state, &result, 0, "", ""); err == nil {
		t.Fatal("write failure returned success")
	}
}
