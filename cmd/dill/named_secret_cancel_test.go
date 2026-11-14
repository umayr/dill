package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

// Deterministic error-classification injection, not a native SDK exposure.
func TestNamedBoundKnownSecretTakesPriorityOverCancellation(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(map[bool]string{true: "bound", false: "legacy"}[bound], func(t *testing.T) {
			f := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			previous := beginCoordinatedOperation
			defer func() { beginCoordinatedOperation = previous }()
			beginCoordinatedOperation = func(context.Context, *coordination.State, *coordination.Target, string, string, []coordination.ResourceInput) (*coordination.Session, error) {
				cancel()
				return nil, config.ErrSecretUnavailable
			}
			args := namedPreviewArgs(f, "up")
			args[2] = operation.Version
			if bound {
				args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			}
			out, stderr, err := captureCommand(t, "", func() error { return runOperation(ctx, args) })
			var result operation.Result
			if e := json.Unmarshal([]byte(out), &result); e != nil {
				t.Fatal(e)
			}
			if exitCodeFor(err) != 1 || result.Effects != "none" || stderr != "" || len(result.Errors) != 1 {
				t.Fatalf("unexpected result %+v", result)
			}
			if bound {
				if result.Outcome != "failed" || result.Target != nil || result.Stack != "" || result.Errors[0].Code != "SECRET_UNAVAILABLE" {
					t.Fatalf("known secret disclosed: %+v", result)
				}
			} else if result.Outcome != "cancelled" || result.Errors[0].Code != "CANCELLED" {
				t.Fatalf("legacy cancellation changed: %+v", result)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("refusal mutated engine")
			}
		})
	}
}
