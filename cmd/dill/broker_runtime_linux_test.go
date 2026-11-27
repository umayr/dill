//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

// Explicit read-only runtime diagnostic; never apply or bypass native guards.
func TestBrokerRuntimeReviewEvidence(t *testing.T) {
	fixture := os.Getenv("DILL_BROKER_RUNTIME_FIXTURE")
	if fixture == "" {
		t.Skip("explicit disposable fixture required")
	}
	root := filepath.Dir(fixture)
	t.Setenv("DILL_PKL_PATH", filepath.Join(root, "pkl"))
	raw, err := os.ReadFile(filepath.Join(fixture, "prepared-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ref brokersecret.Reference
	if json.Unmarshal(raw, &ref) != nil {
		t.Fatal("invalid fixture reference")
	}
	paths, _ := filepath.Glob(filepath.Join(fixture, "stages", "*", "compose.pkl"))
	if len(paths) != 1 {
		t.Fatal("ambiguous source")
	}
	policy, _ := os.ReadFile(filepath.Join(fixture, "brine", "policy.json"))
	var doc struct {
		Targets map[string]struct{ Local struct{ DaemonID string } }
	}
	if json.Unmarshal(policy, &doc) != nil {
		t.Fatal("invalid test policy")
	}
	o := brokerCLIOptions{store: filepath.Join(fixture, "runtime"), file: paths[0], stack: ref.Scope.Stack, socket: "/var/run/docker.sock", daemon: doc.Targets["local"].Local.DaemonID, state: filepath.Join(fixture, "coord"), source: ref.Scope.SourceDigest}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, _, err := brokerCLIConfig(ctx, o, ref.Scope)
	if err != nil {
		t.Fatal("config", err)
	}
	store, err := brokersecret.Open(o.store)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m, err := store.Resolve(ref.Handle, ref.Scope)
	if err != nil {
		t.Fatal(err)
	}
	inv, reason := preflight.ParseBrokerInvocation(o.nativeArgs(false, ""), false)
	if reason != "" {
		t.Fatal(reason)
	}
	state := &operationState{recorder: &operation.Recorder{}, broker: m, config: cfg, ctx: ctx}
	opts := commandOptions{ConfigFile: inv.ConfigFile, ConfigFileSet: true, StackName: inv.Stack, StackNameSet: true, Engine: inv.Engine, EngineSocket: inv.Socket, EngineSocketSet: true, Timeout: inv.Timeout, Quiet: true, NoPrune: inv.NoPrune, operation: state}
	c := &operationCoordination{options: coordinationOptions{directory: inv.StateDir, layout: inv.Layout, daemonID: inv.DaemonID, source: inv.Source}, collectNamed: true, namedReadOnly: true, broker: m}
	state.coordination = c
	outcome := "failed"
	defer func() {
		if c.session != nil {
			_ = c.session.Finish(outcome, "none")
		}
		_ = c.close()
	}()
	if err = c.prepare(ctx, opts, cfg, inv.Operation, inv.Pull); err != nil {
		t.Fatalf("prepare evidence phase: %T: %v", err, err)
	}
	scope, err := c.state.NamedPreflightScope()
	if err != nil {
		t.Fatal("scope", err)
	}
	target := preflight.Target{Engine: c.target.Engine(), EndpointDigest: ref.Scope.EndpointDigest, QualifiedIdentityDigest: ref.Scope.TargetDigest}
	_, selected, err := produceBrokerCapture(ctx, inv, cfg, c.namedInventory, target, scope, m)
	if err != nil {
		t.Fatalf("capture evidence phase: %T: %v", err, err)
	}
	if _, err = selected.Digest(); err != nil {
		t.Fatalf("selection digest phase: %T: %v", err, err)
	}
	outcome = "succeeded"
}
