package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedQualifiedPrepareRetainsSDKObservation(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{true: "present", false: "absent"}[present], func(t *testing.T) {
			f := newCoordinatedDocker(t, present)
			cfg := syntheticConfig()
			useSyntheticConfig(t, cfg)
			c := &operationCoordination{options: coordinationOptions{directory: f.stateDir, layout: coordination.NativeDockerLayout, daemonID: syntheticDaemon, source: "cli"}, collectNamed: true}
			state := &operationState{recorder: &operation.Recorder{}, coordination: c}
			opts := commandOptions{Engine: "docker", EngineSocket: f.socket, EngineSocketSet: true, StackName: "app", StackNameSet: true, Timeout: 3 * time.Second, operation: state}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			defer state.release()
			if err := c.prepare(ctx, opts, cfg, "up", ""); err != nil {
				t.Fatal(err)
			}
			if c.namedInventory == nil {
				t.Fatal("qualified observation missing")
			}
			scope, err := c.state.NamedPreflightScope()
			if err != nil {
				t.Fatal(err)
			}
			target := preflight.Target{Engine: "docker", EndpointDigest: operation.Digest(f.socket), QualifiedIdentityDigest: "sha256:" + c.target.Identity()}
			inv := preflight.Invocation{Operation: "up", Stack: "app", Timeout: opts.Timeout, Source: "cli"}
			capture, selection, err := produceNamedCapture(ctx, inv, cfg, c.namedInventory, target, scope)
			if err != nil {
				t.Fatal(err)
			}
			if capture.compared || len(selection.Resources) != 2 {
				t.Fatal("invalid capture")
			}
			if present && capture.serviceIDs["web"] != strings.Repeat("a", 64) {
				t.Fatal("wrong ID")
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("capture mutated engine", writes)
			}
			if err = c.session.Finish("succeeded", "none"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
